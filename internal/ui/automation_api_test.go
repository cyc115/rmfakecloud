package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// TestAutomationOpenAPIMatchesRoutes the spec lists exactly the automation
// routes the server handles (method + path), and parses as OpenAPI 3.1 with
// an operationId, summary, description and a 2xx example or schema per route
func TestAutomationOpenAPIMatchesRoutes(t *testing.T) {
	a := newAOAPI(t)
	var spec struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(automationOpenAPI, &spec); err != nil {
		t.Fatalf("spec is not JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("openapi = %q", spec.OpenAPI)
	}
	inSpec := map[string]bool{}
	for path, ops := range spec.Paths {
		for method, raw := range ops {
			inSpec[strings.ToUpper(method)+" "+path] = true
			var op struct {
				OperationID string                     `json:"operationId"`
				Summary     string                     `json:"summary"`
				Description string                     `json:"description"`
				Responses   map[string]json.RawMessage `json:"responses"`
			}
			json.Unmarshal(raw, &op)
			if op.OperationID == "" || op.Summary == "" || op.Description == "" {
				t.Errorf("%s %s: operationId, summary and description are required", method, path)
			}
			ok2xx := false
			for code := range op.Responses {
				ok2xx = ok2xx || strings.HasPrefix(code, "2")
			}
			if !ok2xx {
				t.Errorf("%s %s: no 2xx response", method, path)
			}
		}
	}
	served := map[string]bool{}
	for _, r := range a.router.Routes() {
		if r.Path != "/openapi.json" && !strings.HasPrefix(r.Path, "/ui/api/automation") {
			continue
		}
		path := r.Path
		for _, seg := range strings.Split(path, "/") {
			if strings.HasPrefix(seg, ":") {
				path = strings.Replace(path, seg, "{"+seg[1:]+"}", 1)
			}
		}
		served[r.Method+" "+path] = true
	}
	var missing, extra []string
	for k := range served {
		if !inSpec[k] {
			missing = append(missing, k)
		}
	}
	for k := range inSpec {
		if !served[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("routes not in the spec: %v; spec paths the server does not handle: %v", missing, extra)
	}

	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Body.Len() != len(automationOpenAPI) {
		t.Errorf("GET /openapi.json: %d %q %d bytes", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}
}

func TestAutomationAPIToken(t *testing.T) {
	a := newAOAPI(t)
	if code, body := a.do(http.MethodGet, "/ui/api/automation/rules", "Bearer "+apiToken, nil); code != http.StatusOK || body["rules"] == nil {
		t.Fatalf("token: %d %v", code, body)
	}
	if code, _ := a.do(http.MethodGet, "/ui/api/automation/rules", "Bearer wrong-token-0123456789abcdef0123456789", nil); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := a.do(http.MethodGet, "/ui/api/automation/rules", "", nil); code != http.StatusUnauthorized {
		t.Errorf("no auth: %d", code)
	}
	// the login cookie still works
	if code, _ := a.do(http.MethodGet, "/ui/api/automation/rules", a.token("mike", true), nil); code != http.StatusOK {
		t.Errorf("cookie: %d", code)
	}
	// token off: a bearer equal to "" never matches
	a.cfg.AutomationAPIToken = ""
	if code, _ := a.do(http.MethodGet, "/ui/api/automation/rules", "Bearer ", nil); code != http.StatusUnauthorized {
		t.Errorf("token disabled: %d", code)
	}
}

func TestAutomationRuleEndpoints(t *testing.T) {
	a := newAOAPI(t)
	tok := "Bearer " + apiToken
	add := map[string]any{"name": "", "enabled": true,
		"when": map[string]any{"subject": "document", "in": []any{map[string]any{"id": "", "kind": "folder"}},
			"trigger": map[string]any{"type": "created_time", "compare": "within", "amount": 24, "unit": "hours"}},
		"actions": []any{map[string]any{"type": "add_tag", "tag": " new "}}}
	code, body := a.do(http.MethodPost, "/ui/api/automation/rules", tok, add)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	rule := body["rule"].(map[string]any)
	id := rule["id"].(string)
	if id == "" || body["position"].(float64) != 1 || rule["actions"].([]any)[0].(map[string]any)["tag"] != "new" {
		t.Fatalf("created: %v", body)
	}
	// a move rule inserted first
	mv := a.config("dry", 0)["rules"].([]any)[0].(map[string]any)
	delete(mv, "id")
	if code, body := a.do(http.MethodPost, "/ui/api/automation/rules?position=1", tok, mv); code != http.StatusCreated || body["position"].(float64) != 1 {
		t.Fatalf("insert at 1: %d %v", code, body)
	}
	if _, body := a.do(http.MethodGet, "/ui/api/automation/rules", tok, nil); len(body["rules"].([]any)) != 2 ||
		body["rules"].([]any)[1].(map[string]any)["id"] != id {
		t.Fatalf("order: %v", body)
	}
	if code, body := a.do(http.MethodPatch, "/ui/api/automation/rules/"+id, tok, map[string]any{"enabled": false, "name": " New tag "}); code != http.StatusOK ||
		body["rule"].(map[string]any)["enabled"] != false || body["rule"].(map[string]any)["name"] != "New tag" {
		t.Fatalf("patch: %d %v", code, body)
	}
	repl := map[string]any{"enabled": true, "when": add["when"], "conditions": []any{map[string]any{"type": "tag_is", "tag": "new"}},
		"actions": []any{map[string]any{"type": "remove_tag", "tag": "new"}}}
	if code, body := a.do(http.MethodPut, "/ui/api/automation/rules/"+id, tok, repl); code != http.StatusOK ||
		body["rule"].(map[string]any)["actions"].([]any)[0].(map[string]any)["type"] != "remove_tag" || body["rule"].(map[string]any)["id"] != id {
		t.Fatalf("replace: %d %v", code, body)
	}
	if code, body := a.do(http.MethodGet, "/ui/api/automation/rules/"+id, tok, nil); code != http.StatusOK || body["rule"].(map[string]any)["name"] != "" {
		t.Fatalf("get after replace: %d %v", code, body)
	}

	// refusals
	bad := map[string]any{"when": add["when"], "actions": []any{map[string]any{"type": "add_tag", "tag": ""}}}
	if code, body := a.do(http.MethodPost, "/ui/api/automation/rules", tok, bad); code != http.StatusBadRequest || body["ruleIndex"] == nil {
		t.Errorf("invalid rule: %d %v", code, body)
	}
	typo := map[string]any{"when": add["when"], "actions": add["actions"], "condition": []any{}}
	if code, body := a.do(http.MethodPost, "/ui/api/automation/rules", tok, typo); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "unknown field") {
		t.Errorf("unknown field: %d %v", code, body)
	}
	if code, _ := a.do(http.MethodPost, "/ui/api/automation/rules", tok, map[string]any{"id": "x", "when": add["when"], "actions": add["actions"]}); code != http.StatusBadRequest {
		t.Errorf("client id: %d", code)
	}
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		if code, _ := a.do(m, "/ui/api/automation/rules/nope", tok, nil); code != http.StatusNotFound {
			t.Errorf("%s missing rule: %d", m, code)
		}
	}
	if code, _ := a.do(http.MethodPatch, "/ui/api/automation/rules/"+id, tok, map[string]any{"when": map[string]any{}}); code != http.StatusBadRequest {
		t.Errorf("patch outside name/enabled: %d", code)
	}

	if code, body := a.do(http.MethodDelete, "/ui/api/automation/rules/"+id, tok, nil); code != http.StatusOK || body["deleted"] != id {
		t.Fatalf("delete: %d %v", code, body)
	}
	if _, body := a.do(http.MethodGet, "/ui/api/automation", tok, nil); len(body["rules"].([]any)) != 1 {
		t.Fatalf("after delete: %v", body)
	}
}

func TestAutomationCatalogue(t *testing.T) {
	a := newAOAPI(t)
	code, body := a.do(http.MethodGet, "/ui/api/automation/catalogue", "Bearer "+apiToken, nil)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	count := map[string]int{"triggers": len(automation.Triggers), "conditions": len(automation.Conditions), "actions": len(catalogueActions)}
	for kind, n := range count {
		list := body[kind].([]any)
		if len(list) != n {
			t.Errorf("%s: %d entries, want %d", kind, len(list), n)
		}
		for _, e := range list {
			m := e.(map[string]any)
			if m["description"] == "" {
				t.Errorf("%s %s has no description", kind, m["type"])
			}
		}
	}
	for _, e := range body["triggers"].([]any) {
		m := e.(map[string]any)
		if m["type"] == automation.TriggerCreatedTime {
			d := m["defaults"].(map[string]any)
			if d["compare"] != "within" || d["amount"] != float64(24) || d["unit"] != "hours" {
				t.Errorf("created_time defaults = %v", d)
			}
		}
	}
}
