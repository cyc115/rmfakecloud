package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	log "github.com/sirupsen/logrus"
)

// apiToken the AUTOMATION_API_TOKEN of the test server
const apiToken = "test-automation-token-0123456789abcdef"

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
	cfg.AutomationAPIToken, cfg.AutomationAPIUser = apiToken, "mike"
	app := &ReactAppWrapper{cfg: cfg, organizer: autoorganize.NewService(storage, hub.NewHub(), 0), blobs: storage, userStorer: storage}
	router := gin.New()
	router.GET("/openapi.json", serveAutomationOpenAPI)
	api := router.Group("/ui/api")
	auth := api.Group("")
	auth.Use(app.authMiddleware())
	app.registerAutomationRoutes(api, auth)
	app.registerDocTagsRoute(auth)
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
	if bearer, ok := strings.CutPrefix(token, "Bearer "); ok {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if token != "" {
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
			"id": "wsj", "enabled": true,
			"when": map[string]any{"subject": "document",
				"in":      []any{map[string]any{"id": a.inbox, "kind": "folder", "name": "Inbox"}},
				"trigger": map[string]any{"type": "name_matches", "pattern": "WSJ *"}},
			"conditions": []any{},
			"actions":    []any{map[string]any{"type": "move", "to": map[string]any{"id": a.news, "name": "News"}}},
		}},
	}
}

func TestAutomationAPIRefusesSync10AndAnonymous(t *testing.T) {
	a := newAOAPI(t)
	old := a.token("old", false)
	for _, ep := range []struct{ method, path string }{
		{"GET", "/ui/api/automation"},
		{"PUT", "/ui/api/automation"},
		{"GET", "/ui/api/automation/state"},
		{"POST", "/ui/api/automation/preview"},
		{"POST", "/ui/api/automation/run-now"},
		{"GET", "/ui/api/automation/runs"},
		{"GET", "/ui/api/automation/log?tab=moves"},
		{"POST", "/ui/api/automation/undo/x"},
		{"POST", "/ui/api/automation/reapply/x"},
		{"GET", "/ui/api/automation/page-tags"},
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
	if code, body := a.do("GET", "/ui/api/automation/state", a.token("old", true), nil); code != http.StatusBadRequest {
		t.Errorf("profile sync10: %d %v", code, body)
	}
}

func TestAutomationAPIConfigRevision(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	code, cfg := a.do("GET", "/ui/api/automation", tok, nil)
	if code != 200 || cfg["revision"].(float64) != 0 || cfg["settings"].(map[string]any)["mode"] != "dry" {
		t.Fatalf("default config: %d %v", code, cfg)
	}
	code, saved := a.do("PUT", "/ui/api/automation", tok, a.config("dry", 0))
	if code != 200 || saved["revision"].(float64) != 1 {
		t.Fatalf("save: %d %v", code, saved)
	}
	// a second tab with the old revision
	code, body := a.do("PUT", "/ui/api/automation", tok, a.config("live", 0))
	if code != http.StatusConflict || body["revision"].(float64) != 1 {
		t.Fatalf("stale PUT: %d %v", code, body)
	}
	bad := a.config("dry", 1)
	bad["rules"].([]any)[0].(map[string]any)["when"].(map[string]any)["trigger"] = map[string]any{"type": "name_matches", "pattern": "/[/"}
	code, body = a.do("PUT", "/ui/api/automation", tok, bad)
	if code != http.StatusBadRequest || body["ruleIndex"].(float64) != 0 || !strings.Contains(body["error"].(string), "regular expression") {
		t.Fatalf("invalid PUT: %d %v", code, body)
	}
	if _, cfg = a.do("GET", "/ui/api/automation", tok, nil); cfg["revision"].(float64) != 1 {
		t.Fatal("rejected PUTs must not change the config")
	}
}

func TestAutomationAPIStatePreviewRunUndo(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	a.do("PUT", "/ui/api/automation", tok, a.config("dry", 0))

	code, state := a.do("GET", "/ui/api/automation/state", tok, nil)
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

	code, plan := a.do("POST", "/ui/api/automation/preview", tok, nil)
	if code != 200 || len(plan["moves"].([]any)) != 1 {
		t.Fatalf("preview: %d %v", code, plan)
	}
	code, plan = a.do("POST", "/ui/api/automation/preview", tok, map[string]any{"ruleId": "nope"})
	if code != 200 || len(plan["moves"].([]any)) != 0 {
		t.Fatalf("preview other rule: %d %v", code, plan)
	}
	// a draft for the drawer's "Matches now"; finished_reading on a web upload
	// (pageCount 0) is skipped, never guessed
	draft := map[string]any{"rule": map[string]any{"enabled": true,
		"when": map[string]any{"subject": "document",
			"in":      []any{map[string]any{"id": a.inbox, "kind": "folder"}},
			"trigger": map[string]any{"type": "finished_reading", "minPages": 3, "settleHours": 6}},
		"conditions": []any{map[string]any{"type": "kind_is", "kind": "pdf_epub"}},
		"actions":    []any{map[string]any{"type": "move", "to": map[string]any{"createIfMissing": map[string]any{"name": "Read", "parent": "source"}}}}}}
	// a new draft is evaluated after the saved rules: rule 1 claims the doc
	code, plan = a.do("POST", "/ui/api/automation/preview", tok, draft)
	skipped := plan["skipped"].([]any)
	if code != 200 || len(plan["moves"].([]any)) != 0 || len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "claimed" {
		t.Fatalf("new draft preview: %d %v", code, plan)
	}
	if r := plan["rules"].([]any)[0].(map[string]any); r["destName"] != "Inbox / Read" || r["destWillCreate"] != true {
		t.Fatalf("draft destination: %v", r)
	}
	// editing rule 1 (same id) replaces it in place
	draft["rule"].(map[string]any)["id"] = "wsj"
	code, plan = a.do("POST", "/ui/api/automation/preview", tok, draft)
	skipped = plan["skipped"].([]any)
	if code != 200 || len(plan["moves"].([]any)) != 0 || len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "page_count_unknown" {
		t.Fatalf("edited rule preview: %d %v", code, plan)
	}

	// dry run now
	code, res := a.do("POST", "/ui/api/automation/run-now", tok, nil)
	if code != 200 || res["run"].(map[string]any)["mode"] != "DRY" || a.parent(a.doc) != a.inbox {
		t.Fatalf("dry run: %d %v", code, res)
	}
	// live run now
	a.do("PUT", "/ui/api/automation", tok, a.config("live", 1))
	code, res = a.do("POST", "/ui/api/automation/run-now", tok, nil)
	if code != 200 || res["run"].(map[string]any)["moved"].(float64) != 1 || a.parent(a.doc) != a.news {
		t.Fatalf("live run: %d %v", code, res)
	}

	code, l := a.do("GET", "/ui/api/automation/log?tab=moves", tok, nil)
	moves := l["moves"].([]any)
	if code != 200 || len(moves) != 2 || l["runs"] != nil {
		t.Fatalf("moves tab: %d %v", code, l)
	}
	live := moves[0].(map[string]any)
	if live["mode"] != "LIVE" || live["canUndo"] != true || moves[1].(map[string]any)["mode"] != "DRY" {
		t.Fatalf("moves: %v", moves)
	}
	code, l = a.do("GET", "/ui/api/automation/runs", tok, nil)
	if code != 200 || len(l["runs"].([]any)) != 2 || len(l["moves"].([]any)) != 2 {
		t.Fatalf("runs: %d %v", code, l)
	}

	code, body := a.do("POST", "/ui/api/automation/undo/"+live["id"].(string), tok, nil)
	if code != 200 || a.parent(a.doc) != a.inbox {
		t.Fatalf("undo: %d %v", code, body)
	}
	if code, _ := a.do("POST", "/ui/api/automation/undo/"+live["id"].(string), tok, nil); code != http.StatusConflict {
		t.Fatalf("second undo: %d", code)
	}
	if code, _ := a.do("POST", "/ui/api/automation/undo/missing", tok, nil); code != http.StatusNotFound {
		t.Fatalf("unknown entry: %d", code)
	}

	// held after undo: run now does not move it, preview offers re-apply
	a.do("POST", "/ui/api/automation/run-now", tok, nil)
	if a.parent(a.doc) != a.inbox {
		t.Fatal("undone doc moved again")
	}
	_, plan = a.do("POST", "/ui/api/automation/preview", tok, nil)
	held := plan["skipped"].([]any)[0].(map[string]any)
	if held["reason"] != "held" || held["holdId"] == "" {
		t.Fatalf("held: %v", held)
	}
	code, body = a.do("POST", "/ui/api/automation/reapply/"+held["holdId"].(string), tok, nil)
	if code != 200 || a.parent(a.doc) != a.news || body["kind"] != "reapply" {
		t.Fatalf("reapply: %d %v", code, body)
	}
	if code, _ := a.do("POST", "/ui/api/automation/reapply/missing", tok, nil); code != http.StatusNotFound {
		t.Fatalf("unknown hold: %d", code)
	}

	// paused: run now refused, preview still works
	paused := a.config("live", 2)
	paused["settings"].(map[string]any)["enabled"] = false
	if code, _ := a.do("PUT", "/ui/api/automation", tok, paused); code != 200 {
		t.Fatal("pause")
	}
	if code, _ := a.do("POST", "/ui/api/automation/run-now", tok, nil); code != http.StatusConflict {
		t.Fatalf("paused run now: %d", code)
	}
	if code, _ := a.do("POST", "/ui/api/automation/preview", tok, nil); code != 200 {
		t.Fatalf("paused preview: %d", code)
	}
}

func TestWatchablesListsFoldersAndDocuments(t *testing.T) {
	a := newAOAPI(t)
	code, body := a.do("GET", "/ui/api/automation/watchables", a.token("mike", true), nil)
	if code != 200 {
		t.Fatalf("watchables: %d %v", code, body)
	}
	items := body["items"].([]any)
	var got []string
	for _, it := range items {
		m := it.(map[string]any)
		got = append(got, m["kind"].(string)+":"+m["path"].(string))
	}
	want := []string{"folder:Inbox", "document:Inbox / WSJ Weekend", "folder:News"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("items = %v, want %v", got, want)
	}
	doc := items[1].(map[string]any)
	if doc["id"] != a.doc || doc["name"] != "WSJ Weekend" || doc["docType"] != "pdf" {
		t.Fatalf("document item: %v", doc)
	}
	if items[0].(map[string]any)["id"] != a.inbox {
		t.Fatalf("folder item: %v", items[0])
	}
}

func TestPipelineRunNow503WhenOff(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	if code, body := a.do("POST", "/ui/api/automation/pipeline/run-now", tok, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("run-now with page watch off: %d %v", code, body)
	}
	// the drawer still needs the defaults
	code, body := a.do("GET", "/ui/api/automation/pipeline", tok, nil)
	if code != 200 || body["enabled"] != false {
		t.Fatalf("pipeline status off: %d %v", code, body)
	}
	d := body["defaults"].(map[string]any)
	if d["prompt"] != autoorganize.DefaultPipelinePrompt || d["model"] != autoorganize.DefaultPipelineModel {
		t.Fatalf("defaults: %v", d)
	}
	if runs := body["runs"].([]any); len(runs) != 0 {
		t.Fatalf("runs: %v", runs)
	}
	if code, _ := a.do("POST", "/ui/api/automation/pipeline/run-now", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}

func TestAutomationGetPutV2(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	code, cfg := a.do("GET", "/ui/api/automation", tok, nil)
	if code != 200 || cfg["version"].(float64) != 2 || cfg["revision"].(float64) != 0 {
		t.Fatalf("default config: %d %v", code, cfg)
	}
	good := a.config("dry", 0)
	good["version"] = 2
	code, saved := a.do("PUT", "/ui/api/automation", tok, good)
	if code != 200 || saved["revision"].(float64) != 1 || saved["version"].(float64) != 2 {
		t.Fatalf("save: %d %v", code, saved)
	}
	code, body := a.do("PUT", "/ui/api/automation", tok, good) // revision 0 again
	if code != http.StatusConflict || body["revision"].(float64) != 1 || body["error"] == "" {
		t.Fatalf("stale PUT: %d %v", code, body)
	}
	two := a.config("dry", 1)
	two["version"] = 2
	r0 := two["rules"].([]any)[0].(map[string]any)
	r0["actions"] = append(r0["actions"].([]any), map[string]any{"type": "extract_tasks", "prompt": "p", "model": "m"})
	code, body = a.do("PUT", "/ui/api/automation", tok, two)
	if code != http.StatusBadRequest || body["ruleIndex"].(float64) != 0 || body["ruleId"] != "wsj" ||
		!strings.Contains(body["error"].(string), "a rule has exactly one action for now") {
		t.Fatalf("two actions: %d %v", code, body)
	}
	if _, cfg = a.do("GET", "/ui/api/automation", tok, nil); cfg["revision"].(float64) != 1 {
		t.Fatal("rejected PUTs must not change the config")
	}
}

func fileBytes(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// An old UI tab open during the deploy gets 410 and a pointer, and neither
// config file changes (Review Focus 4).
func TestOldRoutesGone(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	good := a.config("dry", 0)
	good["version"] = 2
	if code, _ := a.do("PUT", "/ui/api/automation", tok, good); code != 200 {
		t.Fatal("seed automation.json")
	}
	dir := filepath.Join(a.cfg.DataDir, "users", "mike")
	v1 := filepath.Join(dir, "auto-organize.json")
	if err := os.WriteFile(v1, []byte(`{"revision":3}`), 0600); err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(dir, "automation.json")
	before1, before2 := fileBytes(t, v1), fileBytes(t, v2)

	for _, c := range []struct {
		method, path, location string
		body                   any
	}{
		{"GET", "/ui/api/auto-organize", "/ui/api/automation", nil},
		{"PUT", "/ui/api/auto-organize", "/ui/api/automation", a.config("live", 1)},
		{"POST", "/ui/api/auto-organize/preview", "/ui/api/automation/preview", nil},
		{"GET", "/ui/api/auto-organize/page-tags", "/ui/api/automation/page-tags", nil},
		{"POST", "/ui/api/auto-organize/pipeline/run-now", "/ui/api/automation/pipeline/run-now", nil},
		{"POST", "/ui/api/auto-organize/undo/x", "/ui/api/automation/undo/x", nil},
	} {
		code, body := a.do(c.method, c.path, tok, c.body)
		if code != http.StatusGone || body["location"] != c.location || body["error"] != "this API moved to /ui/api/automation" {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, body)
		}
	}
	// a sync10 session gets the pointer too
	if code, _ := a.do("GET", "/ui/api/auto-organize/state", a.token("old", false), nil); code != http.StatusGone {
		t.Errorf("sync10 old route: %d", code)
	}
	if fileBytes(t, v1) != before1 || fileBytes(t, v2) != before2 {
		t.Fatal("a 410 request changed a config file")
	}
}

// tabletDocs two notebooks in the Inbox (nb1 two pages, nb2 opted out) and a
// tagged page
func (a *aoAPI) tabletDocs() {
	a.t.Helper()
	ft := tablet.New(a.t, a.storage, "mike")
	if err := ft.Load(); err != nil {
		a.t.Fatal(err)
	}
	ft.Notebook("nb1", "Notes", a.inbox)
	ft.Notebook("nb2", "Private", a.inbox)
	for _, p := range []struct{ doc, page string }{{"nb1", "p1"}, {"nb1", "p2"}, {"nb2", "q1"}} {
		if err := ft.SetPage(p.doc, p.page, []byte("rm-"+p.page)); err != nil {
			a.t.Fatal(err)
		}
	}
	ft.Docs["nb2"].Content["tags"] = []any{"no automation"}
	if err := ft.TagPage("nb1", "p2", "todo", time.Now()); err != nil {
		a.t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		a.t.Fatal(err)
	}
}

func (a *aoAPI) pageDraft(trigger map[string]any) map[string]any {
	return map[string]any{"rule": map[string]any{"enabled": true,
		"when": map[string]any{"subject": "page",
			"in":      []any{map[string]any{"id": a.inbox, "kind": "folder"}},
			"trigger": trigger},
		"conditions": []any{},
		"actions":    []any{map[string]any{"type": "extract_tasks", "prompt": "p", "model": "m"}}}}
}

func TestPreviewPageRule(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	a.tabletDocs()

	// page watch is off in this app: the preview still works
	code, body := a.do("POST", "/ui/api/automation/preview", tok, a.pageDraft(map[string]any{"type": "page_changed"}))
	docs, _ := body["docs"].([]any)
	// nb1 and the seeded WSJ upload (no page list); nb2 is opted out
	ids := map[string]bool{}
	for _, d := range docs {
		ids[d.(map[string]any)["id"].(string)] = true
	}
	if code != 200 || body["subject"] != "page" || len(ids) != 2 || !ids["nb1"] || !ids[a.doc] || body["pages"].(float64) != 2 {
		t.Fatalf("page preview: %d %v", code, body)
	}
	if mw, ok := body["missingWatches"].([]any); !ok || len(mw) != 0 {
		t.Fatalf("missingWatches: %v", body["missingWatches"])
	}
	// tagged: only the tagged pages count
	code, body = a.do("POST", "/ui/api/automation/preview", tok, a.pageDraft(map[string]any{"type": "page_tagged", "tag": "todo"}))
	docs, _ = body["docs"].([]any)
	if code != 200 || len(docs) != 1 || body["pages"].(float64) != 1 || docs[0].(map[string]any)["pages"].(float64) != 1 {
		t.Fatalf("tagged preview: %d %v", code, body)
	}
	// a saved page rule by id
	cfg := a.config("dry", 0)
	cfg["version"] = 2
	pg := a.pageDraft(map[string]any{"type": "page_changed"})["rule"].(map[string]any)
	pg["id"] = "pg"
	cfg["rules"] = append(cfg["rules"].([]any), pg)
	if code, b := a.do("PUT", "/ui/api/automation", tok, cfg); code != 200 {
		t.Fatalf("save page rule: %d %v", code, b)
	}
	code, body = a.do("POST", "/ui/api/automation/preview", tok, map[string]any{"ruleId": "pg"})
	if code != 200 || body["subject"] != "page" || body["pages"].(float64) != 2 {
		t.Fatalf("preview by id: %d %v", code, body)
	}
	// no blob storage: 503
	app := &ReactAppWrapper{cfg: a.cfg, organizer: autoorganize.NewService(a.storage, hub.NewHub(), 0)}
	router := gin.New()
	api := router.Group("/ui/api")
	auth := api.Group("")
	auth.Use(app.authMiddleware())
	app.registerAutomationRoutes(api, auth)
	a.router = router
	if code, _ := a.do("POST", "/ui/api/automation/preview", tok, a.pageDraft(map[string]any{"type": "page_changed"})); code != http.StatusServiceUnavailable {
		t.Fatalf("nil blobs: %d", code)
	}
}

func TestPreviewDocumentRuleUnchanged(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	a.do("PUT", "/ui/api/automation", tok, a.config("dry", 0))
	for _, body := range []any{nil, map[string]any{"ruleId": "wsj"}} {
		code, plan := a.do("POST", "/ui/api/automation/preview", tok, body)
		if code != 200 {
			t.Fatalf("preview: %d %v", code, plan)
		}
		keys := []string{}
		for k := range plan {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "at,moves,rules,scanned,skipped" || len(plan["moves"].([]any)) != 1 {
			t.Fatalf("plan keys %v: %v", keys, plan)
		}
	}
}

func TestPageTagsUnderAutomation(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	a.tabletDocs()
	code, body := a.do("GET", "/ui/api/automation/page-tags", tok, nil)
	tags, _ := body["tags"].([]any)
	if code != http.StatusOK || len(tags) != 1 || tags[0].(map[string]any)["name"] != "todo" {
		t.Fatalf("page-tags: %d %v", code, body)
	}
	if code, _ := a.do("GET", "/ui/api/auto-organize/page-tags", tok, nil); code != http.StatusGone {
		t.Fatalf("old page-tags: %d", code)
	}
}
