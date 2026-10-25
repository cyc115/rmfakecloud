package autoorganize

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// fakeDoc a document as the tablet holds it: raw metadata and content maps
type fakeDoc struct {
	id      string
	meta    map[string]any
	content map[string]any
	// noContent the doc has no .content file at all
	noContent bool
	// rawContent overrides content with literal bytes (e.g. corrupt)
	rawContent []byte
}

// fakeTablet writes blobs and commits roots exactly like a sync15 device:
// metadata/content/payload blobs, a doc index per doc, a root index, then the
// root with the generation it last read.
type fakeTablet struct {
	t    *testing.T
	fs   *fs.FileSystemStorage
	uid  string
	docs map[string]*fakeDoc
}

func newHarness(t *testing.T, uid string, sync15 bool) (*fakeTablet, *fs.FileSystemStorage) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}
	storage := fs.NewStorage(cfg)
	user, err := model.NewUser(uid, "password")
	if err != nil {
		t.Fatal(err)
	}
	user.ID = uid
	user.Sync15 = sync15
	if err := storage.RegisterUser(user); err != nil {
		t.Fatal(err)
	}
	return &fakeTablet{t: t, fs: storage, uid: uid, docs: map[string]*fakeDoc{}}, storage
}

func baseMeta(name, parent string, typ common.EntryType, now time.Time) map[string]any {
	return map[string]any{
		"visibleName":      name,
		"type":             string(typ),
		"parent":           parent,
		"createdTime":      ms(now.Add(-30 * 24 * time.Hour)),
		"lastModified":     ms(now.Add(-30 * 24 * time.Hour)),
		"lastOpened":       "",
		"lastOpenedPage":   0,
		"version":          1,
		"pinned":           false,
		"synced":           true,
		"modified":         false,
		"deleted":          false,
		"metadatamodified": false,
		// a field the server's MetadataFile struct does not know
		"lastOpenedTool": "fineliner",
	}
}

func (ft *fakeTablet) folder(id, name, parent string) *fakeDoc {
	d := &fakeDoc{id: id, meta: baseMeta(name, parent, common.CollectionType, time.Now()), content: map[string]any{"tags": []any{}}}
	ft.docs[id] = d
	return d
}

func (ft *fakeTablet) pdf(id, name, parent string, pageCount, lastOpenedPage int, lastOpened time.Time) *fakeDoc {
	d := &fakeDoc{id: id, meta: baseMeta(name, parent, common.DocumentType, time.Now())}
	d.meta["lastOpenedPage"] = lastOpenedPage
	if !lastOpened.IsZero() {
		d.meta["lastOpened"] = ms(lastOpened)
	}
	d.content = map[string]any{
		"fileType":       "pdf",
		"pageCount":      pageCount,
		"lastOpenedPage": 0,
		"textScale":      0.8,
		"tags":           []any{},
	}
	ft.docs[id] = d
	return d
}

func (ft *fakeTablet) notebook(id, name, parent string) *fakeDoc {
	d := &fakeDoc{id: id, meta: baseMeta(name, parent, common.DocumentType, time.Now())}
	d.content = map[string]any{"fileType": "notebook", "pageCount": 2, "tags": []any{}}
	ft.docs[id] = d
	return d
}

func (ft *fakeTablet) write(data []byte) (string, int64) {
	hash, size, err := models.Hash(bytes.NewReader(data))
	if err != nil {
		ft.t.Fatal(err)
	}
	if err := ft.fs.BlobStorage(ft.uid).Write(hash, bytes.NewReader(data)); err != nil {
		ft.t.Fatal(err)
	}
	return hash, size
}

// pull refreshes every doc's metadata from the server, like a tablet sync
// that downloads the server's changes before uploading its own
func (ft *fakeTablet) pull() {
	tree, err := ft.fs.GetCachedTree(ft.uid)
	if err != nil {
		ft.t.Fatal(err)
	}
	bs := ft.fs.BlobStorage(ft.uid)
	for _, hd := range tree.Docs {
		d, ok := ft.docs[hd.EntryName]
		if !ok {
			d = &fakeDoc{id: hd.EntryName, content: map[string]any{"tags": []any{}}}
			ft.docs[hd.EntryName] = d
		}
		for _, f := range hd.Files {
			if !f.IsMetadata() {
				continue
			}
			r, err := bs.GetReader(f.Hash)
			if err != nil {
				ft.t.Fatal(err)
			}
			b, _ := io.ReadAll(r)
			r.Close()
			m := map[string]any{}
			if err := json.Unmarshal(b, &m); err != nil {
				ft.t.Fatal(err)
			}
			d.meta = m
		}
	}
}

// sync uploads every doc and commits a new root
func (ft *fakeTablet) sync() {
	ft.t.Helper()
	bs := ft.fs.BlobStorage(ft.uid)
	tree := &models.HashTree{}
	for id, d := range ft.docs {
		hd := models.NewHashDoc("", id, common.DocumentType)
		mb, _ := json.Marshal(d.meta)
		h, size := ft.write(mb)
		hd.AddFile(models.NewHashEntry(h, id+".metadata", size))
		if !d.noContent {
			cb := d.rawContent
			if cb == nil {
				cb, _ = json.Marshal(d.content)
			}
			h, size = ft.write(cb)
			hd.AddFile(models.NewHashEntry(h, id+".content", size))
		}
		if d.meta["type"] == string(common.DocumentType) {
			h, size = ft.write([]byte("%PDF-1.4 " + id))
			hd.AddFile(models.NewHashEntry(h, id+".pdf", size))
		}
		idx, err := hd.IndexReader()
		if err != nil {
			ft.t.Fatal(err)
		}
		ib, _ := io.ReadAll(idx)
		// indexes are stored under the HashEntries hash, like the device does
		if err := bs.Write(hd.Hash, bytes.NewReader(ib)); err != nil {
			ft.t.Fatal(err)
		}
		tree.Docs = append(tree.Docs, hd)
	}
	tree.Rehash()
	ri, _ := tree.RootIndex()
	rb, _ := io.ReadAll(ri)
	if err := bs.Write(tree.Hash, bytes.NewReader(rb)); err != nil {
		ft.t.Fatal(err)
	}
	_, gen, err := bs.GetRootIndex()
	if err != nil {
		ft.t.Fatal(err)
	}
	if _, err := bs.WriteRootIndex(gen, tree.Hash); err != nil {
		ft.t.Fatal(err)
	}
}

// serverDoc the doc as the server stores it, rebuilt from blobs only
func (ft *fakeTablet) serverDoc(id string) *models.HashDoc {
	ft.t.Helper()
	os.Remove(filepath.Join(ft.fs.UserDataPath(ft.uid), ".tree"))
	tree, err := ft.fs.GetCachedTree(ft.uid)
	if err != nil {
		ft.t.Fatal(err)
	}
	d, err := tree.FindDoc(id)
	if err != nil {
		return nil
	}
	return d
}

// serverMetaRaw the raw metadata blob of a doc on the server
func (ft *fakeTablet) serverMetaRaw(id string) map[string]any {
	ft.t.Helper()
	d := ft.serverDoc(id)
	if d == nil {
		ft.t.Fatalf("doc %s not on server", id)
	}
	for _, f := range d.Files {
		if f.IsMetadata() {
			r, err := ft.fs.BlobStorage(ft.uid).GetReader(f.Hash)
			if err != nil {
				ft.t.Fatal(err)
			}
			defer r.Close()
			m := map[string]any{}
			json.NewDecoder(r).Decode(&m)
			return m
		}
	}
	return nil
}

func (ft *fakeTablet) parentOf(id string) string {
	ft.t.Helper()
	d := ft.serverDoc(id)
	if d == nil {
		ft.t.Fatalf("doc %s not on server", id)
	}
	return d.Parent
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
