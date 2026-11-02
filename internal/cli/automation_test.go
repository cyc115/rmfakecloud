package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

const gold = "../autoorganize/testdata/"

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decode(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return v
}

// setup a store with user "u" holding the golden v1 file
func setup(t *testing.T) (*autoorganize.Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "u"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "u", autoorganize.V1ConfigFileName)
	if err := os.WriteFile(p, readFile(t, gold+"v1-golden.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}
	return autoorganize.NewStore(func(uid string) string { return filepath.Join(dir, uid) }, 0), filepath.Join(dir, "u")
}

func run(store *autoorganize.Store, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := runAutomation(args, store, &out, &errb)
	return code, out.String(), errb.String()
}

type entry struct {
	size int64
	mod  time.Time
}

func listing(t *testing.T, dir string) map[string]entry {
	t.Helper()
	m := map[string]entry{}
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		fi, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		m[e.Name()] = entry{fi.Size(), fi.ModTime()}
	}
	return m
}

func TestMigrateDryRunPrintsV2WritesNothing(t *testing.T) {
	store, dir := setup(t)
	before := listing(t, dir)
	code, out, errs := run(store, "migrate", "--dry-run", "--user", "u")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !reflect.DeepEqual(decode(t, []byte(out)), decode(t, readFile(t, gold+"v2-golden.json"))) {
		t.Fatalf("stdout:\n%s", out)
	}
	if after := listing(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("directory changed:\n%v\n%v", before, after)
	}
}

func TestMigrateWithoutDryRunRefuses(t *testing.T) {
	store, dir := setup(t)
	before := listing(t, dir)
	code, out, errs := run(store, "migrate", "--user", "u")
	if code != 2 || out != "" || !strings.Contains(errs, "only --dry-run is supported; the server migrates on start") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if !reflect.DeepEqual(before, listing(t, dir)) {
		t.Fatal("directory changed")
	}
}

func TestMigrateDryRunAfterMigration(t *testing.T) {
	store, dir := setup(t)
	if _, err := store.LoadConfig("u"); err != nil {
		t.Fatal(err)
	}
	before := listing(t, dir)
	code, out, errs := run(store, "migrate", "--dry-run", "--user", "u")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(errs, "note: automation.json already exists; showing the migration of auto-organize.json") {
		t.Fatalf("stderr %q", errs)
	}
	if !reflect.DeepEqual(decode(t, []byte(out)), decode(t, readFile(t, gold+"v2-golden.json"))) {
		t.Fatalf("stdout:\n%s", out)
	}
	if !reflect.DeepEqual(before, listing(t, dir)) {
		t.Fatal("directory changed")
	}
}

func TestMigrateDryRunReportsDisabledRules(t *testing.T) {
	store, dir := setup(t)
	var v1 map[string]any
	if err := json.Unmarshal(readFile(t, gold+"v1-golden.json"), &v1); err != nil {
		t.Fatal(err)
	}
	rules := v1["rules"].([]any)
	bad := map[string]any{}
	for k, v := range rules[0].(map[string]any) {
		bad[k] = v
	}
	bad["id"] = "bad-rule"
	delete(bad, "scope")
	v1["rules"] = append(rules, bad)
	b, _ := json.Marshal(v1)
	if err := os.WriteFile(filepath.Join(dir, autoorganize.V1ConfigFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := run(store, "migrate", "--dry-run", "--user", "u")
	if code != 0 || !strings.Contains(errs, "rule 3 (bad-rule) would be disabled: v1 skipped it as an invalid rule") || !strings.HasPrefix(out, "{") {
		t.Fatalf("exit %d err %q out %q", code, errs, out)
	}
}

func TestRollbackWritesV1AndRenamesV2(t *testing.T) {
	store, dir := setup(t)
	v1Path := filepath.Join(dir, autoorganize.V1ConfigFileName)
	oldV1 := readFile(t, v1Path)
	cfg, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := run(store, "rollback", "--user", "u")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, v1Path) {
		t.Fatalf("stdout %q", out)
	}
	v1 := &autoorganize.V1Config{}
	if err := json.Unmarshal(readFile(t, v1Path), v1); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(); err != nil {
		t.Fatalf("v1 invalid: %v", err)
	}
	if !bytes.Equal(readFile(t, v1Path+".bak"), oldV1) {
		t.Fatal(".bak is not the old v1 file")
	}
	if _, err := os.Stat(filepath.Join(dir, autoorganize.ConfigFileName)); !os.IsNotExist(err) {
		t.Fatalf("automation.json still there: %v", err)
	}
	renamed, _ := filepath.Glob(filepath.Join(dir, autoorganize.ConfigFileName+".rolled-back-*"))
	if len(renamed) != 1 {
		t.Fatalf("renamed files %v", renamed)
	}
	again, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Rules) != len(cfg.Rules) {
		t.Fatalf("rules %d, want %d", len(again.Rules), len(cfg.Rules))
	}
	for i := range cfg.Rules {
		if again.Rules[i].ID != cfg.Rules[i].ID {
			t.Errorf("rule %d id %q, want %q", i, again.Rules[i].ID, cfg.Rules[i].ID)
		}
	}
}

// addInexpressible adds a 2-action rule to automation.json (v1 has no form
// for it); returns its id
func addInexpressible(t *testing.T, store *autoorganize.Store, dir string) string {
	t.Helper()
	if _, err := store.LoadConfig("u"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, autoorganize.ConfigFileName)
	var v2 map[string]any
	if err := json.Unmarshal(readFile(t, p), &v2); err != nil {
		t.Fatal(err)
	}
	rules := v2["rules"].([]any)
	r := map[string]any{}
	for k, v := range rules[0].(map[string]any) {
		r[k] = v
	}
	r["id"] = "two-actions"
	acts := r["actions"].([]any)
	r["actions"] = append(acts, acts[0])
	v2["rules"] = append(rules, r)
	b, _ := json.MarshalIndent(v2, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return "two-actions"
}

func TestRollbackRefusesInexpressible(t *testing.T) {
	store, dir := setup(t)
	id := addInexpressible(t, store, dir)
	before := listing(t, dir)
	v1b := readFile(t, filepath.Join(dir, autoorganize.V1ConfigFileName))
	v2b := readFile(t, filepath.Join(dir, autoorganize.ConfigFileName))
	code, _, errs := run(store, "rollback", "--user", "u")
	if code != 1 || !strings.Contains(errs, "rule 3 ("+id+"): ") {
		t.Fatalf("exit %d err %q", code, errs)
	}
	if !bytes.Equal(v1b, readFile(t, filepath.Join(dir, autoorganize.V1ConfigFileName))) ||
		!bytes.Equal(v2b, readFile(t, filepath.Join(dir, autoorganize.ConfigFileName))) {
		t.Fatal("a file changed")
	}
	if !reflect.DeepEqual(before, listing(t, dir)) {
		t.Fatal("directory changed")
	}
}

func TestRollbackForceKeepsFullV2Copy(t *testing.T) {
	store, dir := setup(t)
	id := addInexpressible(t, store, dir)
	v2b := readFile(t, filepath.Join(dir, autoorganize.ConfigFileName))
	code, _, errs := run(store, "rollback", "--user", "u", "--force")
	if code != 0 || !strings.Contains(errs, "rule 3 ("+id+"): ") {
		t.Fatalf("exit %d err %q", code, errs)
	}
	renamed, _ := filepath.Glob(filepath.Join(dir, autoorganize.ConfigFileName+".rolled-back-*"))
	if len(renamed) != 1 || !bytes.Equal(readFile(t, renamed[0]), v2b) {
		t.Fatalf("renamed %v does not hold the full v2 file", renamed)
	}
	v1 := &autoorganize.V1Config{}
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, autoorganize.V1ConfigFileName)), v1); err != nil {
		t.Fatal(err)
	}
	if len(v1.Rules) != 2 || v1.Validate() != nil {
		t.Fatalf("v1 rules %d, err %v", len(v1.Rules), v1.Validate())
	}
}

func TestUnknownUserErrors(t *testing.T) {
	store, _ := setup(t)
	for _, args := range [][]string{
		{"rollback", "--user", "nobody"},
		{"rollback", "--user", "nobody", "--force"},
	} {
		if code, _, errs := run(store, args...); code != 1 || errs == "" {
			t.Errorf("%v: exit %d err %q", args, code, errs)
		}
	}
}

// a user with no auto-organize.json has nothing to migrate: a note, exit 0
// (also when automation.json already exists and the v1 file is gone)
func TestMigrateDryRunNothingToMigrate(t *testing.T) {
	store, dir := setup(t)
	want := "nothing to migrate for nobody: no auto-organize.json\n"
	if code, out, errs := run(store, "migrate", "--dry-run", "--user", "nobody"); code != 0 || out != "" || errs != want {
		t.Errorf("unknown user: exit %d out %q err %q", code, out, errs)
	}
	if _, err := store.LoadConfig("u"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, autoorganize.V1ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	want = "nothing to migrate for u: no auto-organize.json\n"
	if code, out, errs := run(store, "migrate", "--dry-run", "--user", "u"); code != 0 || out != "" || errs != want {
		t.Errorf("v2 only: exit %d out %q err %q", code, out, errs)
	}
}

func TestAutomationUsageErrors(t *testing.T) {
	store, _ := setup(t)
	for _, args := range [][]string{{}, {"bogus"}, {"migrate", "--dry-run"}, {"rollback"}} {
		if code, _, _ := run(store, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestRollbackWarnsToStopServer(t *testing.T) {
	const w = "stop the rmfakecloud server first: a running server re-migrates and undoes the rollback"
	store, _ := setup(t)
	if _, _, errs := run(store, "rollback"); !strings.Contains(errs, w) {
		t.Errorf("usage lacks the warning: %q", errs)
	}
	if _, _, errs := run(store, "rollback", "--user", "nobody"); !strings.Contains(errs, w) {
		t.Errorf("no warning before rollback: %q", errs)
	}
	if !strings.Contains(Usage(), "stop the rmfakecloud server first") {
		t.Error("Usage() lacks the warning")
	}
}
