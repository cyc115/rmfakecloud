package app

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func newTestApp(t *testing.T) (*App, *fs.FileSystemStorage) {
	t.Helper()
	log.SetLevel(log.WarnLevel)
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DataDir: t.TempDir(), Port: freePort(t), MQTTPort: freePort(t),
		JWTSecretKey: []byte("secret"), StorageURL: "http://localhost"}
	storage := fs.NewStorage(cfg)
	user, err := model.NewUser("mike", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.RegisterUser(user); err != nil {
		t.Fatal(err)
	}
	a := NewApp(cfg)
	a.organizer.Debounce = 10 * time.Millisecond
	return &a, storage
}

func runCount(t *testing.T, a *App) (int, string) {
	st, err := a.organizer.Store().LoadState("mike")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Runs) == 0 {
		return 0, ""
	}
	return len(st.Runs), st.Runs[0].Trigger
}

func waitRuns(t *testing.T, a *App, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, trig := runCount(t, a); got >= n {
			if trig != autoorganize.TriggerTabletSync {
				t.Fatalf("trigger %q", trig)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := runCount(t, a)
	t.Fatalf("runs = %d, want %d", got, n)
}

func waitLoop(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !a.organizer.Status("mike", autoorganize.NewState()).LoopAlive {
		if time.Now().After(deadline) {
			t.Fatal("sweeper loop did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func deviceCtx(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(userIDKey, "mike")
	c.Set(deviceIDKey, "tablet-1")
	return c, w
}

// Every device root-write path schedules a debounced sweep.
func TestRootWritesTriggerSweep(t *testing.T) {
	a, storage := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); a.organizer.Wait() }()
	a.organizer.Start(ctx)
	waitLoop(t, a)

	c, w := deviceCtx("POST", "/api/v1/sync-complete", "")
	a.syncComplete(c)
	if w.Code != 200 {
		t.Fatalf("sync-complete: %d", w.Code)
	}
	waitRuns(t, a, 1)

	c, w = deviceCtx("POST", "/sync/v2/sync-complete", `{"generation": 1}`)
	a.syncCompleteV2(c)
	if w.Code != 200 {
		t.Fatalf("sync-complete v2: %d", w.Code)
	}
	waitRuns(t, a, 2)

	c, w = deviceCtx("PUT", "/sync/v3/root", `{"hash":"abc","generation":0,"broadcast":false}`)
	a.syncUpdateRootV3(c)
	if w.Code != 200 {
		t.Fatalf("v3 root: %d %s", w.Code, w.Body.String())
	}
	waitRuns(t, a, 3)

	// the legacy signed blob route, through the router
	blobURL, _, err := storage.GetBlobURL("mike", "root", true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(blobURL)
	req := httptest.NewRequest("PUT", u.Path+"?"+u.RawQuery, bytes.NewBufferString("def"))
	req.Header.Set("x-goog-if-generation-match", "1")
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("blob upload: %d %s", rec.Code, rec.Body.String())
	}
	waitRuns(t, a, 4)

	// a non-root blob does not trigger
	blobURL, _, _ = storage.GetBlobURL("mike", "somehash", true)
	u, _ = url.Parse(blobURL)
	req = httptest.NewRequest("PUT", u.Path+"?"+u.RawQuery, bytes.NewBufferString("x"))
	a.router.ServeHTTP(httptest.NewRecorder(), req)
	time.Sleep(80 * time.Millisecond)
	if got, _ := runCount(t, a); got != 4 {
		t.Fatalf("non-root blob triggered a sweep: %d runs", got)
	}
}

// Start runs the sweeper before serving; Stop cancels it before shutdown.
func TestAppLifecycleStartsAndStopsSweeper(t *testing.T) {
	a, _ := newTestApp(t)
	go a.Start()
	waitLoop(t, a)
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:" + a.cfg.Port + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.Stop()
	// Stop waits for the sweeper, so it is already gone when Stop returns
	done := make(chan struct{})
	go func() { a.organizer.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("sweeper still running after Stop returned")
	}
}
