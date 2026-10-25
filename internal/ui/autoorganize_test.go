package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	log "github.com/sirupsen/logrus"
)

type aoAPI struct {
	t       *testing.T
	router  *gin.Engine
	cfg     *config.Config
	storage *fs.FileSystemStorage
	inbox   string
	news    string
	doc     string
}

func newAOAPI(t *testing.T) *aoAPI {
	t.Helper()
	log.SetLevel(log.WarnLevel)
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DataDir: t.TempDir(), JWTSecretKey: []byte("test-secret")}
	storage := fs.NewStorage(cfg)
	for _, u := range []struct {
		id     string
		sync15 bool
	}{{"mike", true}, {"old", false}} {
		user, err := model.NewUser(u.id, "pw")
		if err != nil {
			t.Fatal(err)
		}
		user.Sync15 = u.sync15
		if err := storage.RegisterUser(user); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := storage.CreateBlobFolder("mike", "Inbox", "")
	if err != nil {
		t.Fatal(err)
	}
	news, err := storage.CreateBlobFolder("mike", "News", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := storage.CreateBlobDocument("mike", "WSJ Weekend.pdf", inbox.ID, strings.NewReader("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	app := &ReactAppWrapper{cfg: cfg, organizer: autoorganize.NewService(storage, hub.NewHub(), 0)}
	router := gin.New()
	auth := router.Group("/ui/api").Group("")
	auth.Use(app.authMiddleware())
	app.registerAutoOrganizeRoutes(auth)
	return &aoAPI{t: t, router: router, cfg: cfg, storage: storage, inbox: inbox.ID, news: news.ID, doc: doc.ID}
}

func (a *aoAPI) token(uid string, sync15 bool) string {
	scopes := ""
	if sync15 {
		scopes = isSync15Key
	}
	tok, err := common.SignClaims(&WebUserClaims{UserID: uid, BrowserID: "b", Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{Audience: []string{WebUsage}}}, a.cfg.JWTSecretKey)
	if err != nil {
		a.t.Fatal(err)
	}
	return tok
}

func (a *aoAPI) do(method, path, token string, body any) (int, map[string]any) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (a *aoAPI) parent(id string) string {
	tree, err := a.storage.GetCachedTree("mike")
	if err != nil {
		a.t.Fatal(err)
	}
	d, err := tree.FindDoc(id)
	if err != nil {
		a.t.Fatal(err)
	}
	return d.Parent
}

func (a *aoAPI) config(mode string, rev float64) map[string]any {
	return map[string]any{
		"revision": rev,
		"settings": map[string]any{"enabled": true, "mode": mode, "intervalMinutes": 15},
		"rules": []any{map[string]any{
			"id": "wsj", "enabled": true, "scope": "all",
			"source":      map[string]any{"id": a.inbox, "name": "Inbox"},
			"trigger":     map[string]any{"type": "name_matches", "pattern": "WSJ *"},
			"destination": map[string]any{"id": a.news, "name": "News"},
			"action":      map[string]any{"type": "move"},
		}},
	}
}

func TestAutoOrganizeAPIRefusesSync10AndAnonymous(t *testing.T) {
	a := newAOAPI(t)
	old := a.token("old", false)
	for _, ep := range []struct{ method, path string }{
		{"GET", "/ui/api/auto-organize"},
		{"PUT", "/ui/api/auto-organize"},
		{"GET", "/ui/api/auto-organize/state"},
		{"POST", "/ui/api/auto-organize/preview"},
		{"POST", "/ui/api/auto-organize/run-now"},
		{"GET", "/ui/api/auto-organize/runs"},
		{"GET", "/ui/api/auto-organize/log?tab=moves"},
		{"POST", "/ui/api/auto-organize/undo/x"},
		{"POST", "/ui/api/auto-organize/reapply/x"},
	} {
		code, body := a.do(ep.method, ep.path, old, nil)
		if code != http.StatusBadRequest || body["error"] != "auto-organize requires sync15" {
			t.Errorf("sync10 %s %s: %d %v", ep.method, ep.path, code, body)
		}
		if code, _ := a.do(ep.method, ep.path, "", nil); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d", ep.method, ep.path, code)
		}
	}
	// a sync15 cookie for a user whose profile is sync10 is refused by the service too
	if code, body := a.do("GET", "/ui/api/auto-organize/state", a.token("old", true), nil); code != http.StatusBadRequest {
		t.Errorf("profile sync10: %d %v", code, body)
	}
}

func TestAutoOrganizeAPIConfigRevision(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	code, cfg := a.do("GET", "/ui/api/auto-organize", tok, nil)
	if code != 200 || cfg["revision"].(float64) != 0 || cfg["settings"].(map[string]any)["mode"] != "dry" {
		t.Fatalf("default config: %d %v", code, cfg)
	}
	code, saved := a.do("PUT", "/ui/api/auto-organize", tok, a.config("dry", 0))
	if code != 200 || saved["revision"].(float64) != 1 {
		t.Fatalf("save: %d %v", code, saved)
	}
	// a second tab with the old revision
	code, body := a.do("PUT", "/ui/api/auto-organize", tok, a.config("live", 0))
	if code != http.StatusConflict || body["revision"].(float64) != 1 {
		t.Fatalf("stale PUT: %d %v", code, body)
	}
	bad := a.config("dry", 1)
	bad["rules"].([]any)[0].(map[string]any)["trigger"] = map[string]any{"type": "name_matches", "pattern": "/[/"}
	code, body = a.do("PUT", "/ui/api/auto-organize", tok, bad)
	if code != http.StatusBadRequest || body["ruleIndex"].(float64) != 0 || !strings.Contains(body["error"].(string), "regular expression") {
		t.Fatalf("invalid PUT: %d %v", code, body)
	}
	if _, cfg = a.do("GET", "/ui/api/auto-organize", tok, nil); cfg["revision"].(float64) != 1 {
		t.Fatal("rejected PUTs must not change the config")
	}
}

func TestAutoOrganizeAPIStatePreviewRunUndo(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	a.do("PUT", "/ui/api/auto-organize", tok, a.config("dry", 0))

	code, state := a.do("GET", "/ui/api/auto-organize/state", tok, nil)
	if code != 200 {
		t.Fatalf("state: %d %v", code, state)
	}
	folders := state["folders"].([]any)
	if len(folders) != 3 || folders[0].(map[string]any)["name"] != "My files" {
		t.Fatalf("folders: %v", folders)
	}
	rule := state["rules"].([]any)[0].(map[string]any)
	if rule["matches"].(float64) != 1 || rule["sourceName"] != "Inbox" || rule["destName"] != "News" {
		t.Fatalf("rule view: %v", rule)
	}

	code, plan := a.do("POST", "/ui/api/auto-organize/preview", tok, nil)
	if code != 200 || len(plan["moves"].([]any)) != 1 {
		t.Fatalf("preview: %d %v", code, plan)
	}
	code, plan = a.do("POST", "/ui/api/auto-organize/preview", tok, map[string]any{"ruleId": "nope"})
	if code != 200 || len(plan["moves"].([]any)) != 0 {
		t.Fatalf("preview other rule: %d %v", code, plan)
	}
	// a draft for the drawer's "Matches now"; finished_reading on a web upload
	// (pageCount 0) is skipped, never guessed
	draft := map[string]any{"rule": map[string]any{"enabled": true, "scope": "pdf_epub",
		"source":      map[string]any{"id": a.inbox},
		"trigger":     map[string]any{"type": "finished_reading", "minPages": 3, "settleHours": 6},
		"destination": map[string]any{"createIfMissing": map[string]any{"name": "Read", "parent": "source"}},
		"action":      map[string]any{"type": "move"}}}
	// a new draft is evaluated after the saved rules: rule 1 claims the doc
	code, plan = a.do("POST", "/ui/api/auto-organize/preview", tok, draft)
	skipped := plan["skipped"].([]any)
	if code != 200 || len(plan["moves"].([]any)) != 0 || len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "claimed" {
		t.Fatalf("new draft preview: %d %v", code, plan)
	}
	if r := plan["rules"].([]any)[0].(map[string]any); r["destName"] != "Inbox / Read" || r["destWillCreate"] != true {
		t.Fatalf("draft destination: %v", r)
	}
	// editing rule 1 (same id) replaces it in place
	draft["rule"].(map[string]any)["id"] = "wsj"
	code, plan = a.do("POST", "/ui/api/auto-organize/preview", tok, draft)
	skipped = plan["skipped"].([]any)
	if code != 200 || len(plan["moves"].([]any)) != 0 || len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "page_count_unknown" {
		t.Fatalf("edited rule preview: %d %v", code, plan)
	}

	// dry run now
	code, res := a.do("POST", "/ui/api/auto-organize/run-now", tok, nil)
	if code != 200 || res["run"].(map[string]any)["mode"] != "DRY" || a.parent(a.doc) != a.inbox {
		t.Fatalf("dry run: %d %v", code, res)
	}
	// live run now
	a.do("PUT", "/ui/api/auto-organize", tok, a.config("live", 1))
	code, res = a.do("POST", "/ui/api/auto-organize/run-now", tok, nil)
	if code != 200 || res["run"].(map[string]any)["moved"].(float64) != 1 || a.parent(a.doc) != a.news {
		t.Fatalf("live run: %d %v", code, res)
	}

	code, l := a.do("GET", "/ui/api/auto-organize/log?tab=moves", tok, nil)
	moves := l["moves"].([]any)
	if code != 200 || len(moves) != 2 || l["runs"] != nil {
		t.Fatalf("moves tab: %d %v", code, l)
	}
	live := moves[0].(map[string]any)
	if live["mode"] != "LIVE" || live["canUndo"] != true || moves[1].(map[string]any)["mode"] != "DRY" {
		t.Fatalf("moves: %v", moves)
	}
	code, l = a.do("GET", "/ui/api/auto-organize/runs", tok, nil)
	if code != 200 || len(l["runs"].([]any)) != 2 || len(l["moves"].([]any)) != 2 {
		t.Fatalf("runs: %d %v", code, l)
	}

	code, body := a.do("POST", "/ui/api/auto-organize/undo/"+live["id"].(string), tok, nil)
	if code != 200 || a.parent(a.doc) != a.inbox {
		t.Fatalf("undo: %d %v", code, body)
	}
	if code, _ := a.do("POST", "/ui/api/auto-organize/undo/"+live["id"].(string), tok, nil); code != http.StatusConflict {
		t.Fatalf("second undo: %d", code)
	}
	if code, _ := a.do("POST", "/ui/api/auto-organize/undo/missing", tok, nil); code != http.StatusNotFound {
		t.Fatalf("unknown entry: %d", code)
	}

	// held after undo: run now does not move it, preview offers re-apply
	a.do("POST", "/ui/api/auto-organize/run-now", tok, nil)
	if a.parent(a.doc) != a.inbox {
		t.Fatal("undone doc moved again")
	}
	_, plan = a.do("POST", "/ui/api/auto-organize/preview", tok, nil)
	held := plan["skipped"].([]any)[0].(map[string]any)
	if held["reason"] != "held" || held["holdId"] == "" {
		t.Fatalf("held: %v", held)
	}
	code, body = a.do("POST", "/ui/api/auto-organize/reapply/"+held["holdId"].(string), tok, nil)
	if code != 200 || a.parent(a.doc) != a.news || body["kind"] != "reapply" {
		t.Fatalf("reapply: %d %v", code, body)
	}
	if code, _ := a.do("POST", "/ui/api/auto-organize/reapply/missing", tok, nil); code != http.StatusNotFound {
		t.Fatalf("unknown hold: %d", code)
	}

	// paused: run now refused, preview still works
	paused := a.config("live", 2)
	paused["settings"].(map[string]any)["enabled"] = false
	if code, _ := a.do("PUT", "/ui/api/auto-organize", tok, paused); code != 200 {
		t.Fatal("pause")
	}
	if code, _ := a.do("POST", "/ui/api/auto-organize/run-now", tok, nil); code != http.StatusConflict {
		t.Fatalf("paused run now: %d", code)
	}
	if code, _ := a.do("POST", "/ui/api/auto-organize/preview", tok, nil); code != 200 {
		t.Fatalf("paused preview: %d", code)
	}
}
