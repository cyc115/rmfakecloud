package autoorganize

import (
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

// fakeTablet the shared fake device; see internal/testkit/tablet
type fakeTablet = tablet.Tablet

func newHarness(t *testing.T, uid string, sync15 bool) (*fakeTablet, *fs.FileSystemStorage) {
	t.Helper()
	storage := tablet.NewStorage(t.TempDir())
	if err := tablet.RegisterUser(storage, uid, sync15); err != nil {
		t.Fatal(err)
	}
	return tablet.New(t, storage, uid), storage
}

// fakeNotifier records NotifySync calls
type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
}

func (n *fakeNotifier) NotifySync(uid, deviceID string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, uid+"|"+deviceID)
	return "1"
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

// clock a settable test clock
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newService(t *testing.T, storage *fs.FileSystemStorage) (*Service, *fakeNotifier, *clock) {
	n := &fakeNotifier{}
	svc := NewService(storage, n, DefaultInterval)
	c := &clock{now: time.Now()}
	svc.Now = c.Now
	return svc, n, c
}

func saveConfig(t *testing.T, svc *Service, uid string, mode string, rules ...Rule) *Config {
	t.Helper()
	cur, err := svc.Store().LoadConfig(uid)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Revision: cur.Revision, Settings: Settings{Enabled: true, Mode: mode, IntervalMinutes: 15}, Rules: rules}
	saved, err := svc.Store().SaveConfig(uid, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
