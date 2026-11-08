package ui

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// automationOpenAPI the OpenAPI 3.1 description of the automation API, served
// at GET /openapi.json. It changes in the same commit as any route below
// (TestAutomationOpenAPIMatchesRoutes).
//
//go:embed automation_openapi.json
var automationOpenAPI []byte

// automationAuth the automation API accepts the web UI's login (cookie or JWT
// bearer) or, for agents and scripts, the AUTOMATION_API_TOKEN bearer, which
// acts as AUTOMATION_API_USER.
func (app *ReactAppWrapper) automationAuth() gin.HandlerFunc {
	login := app.authMiddleware()
	return func(c *gin.Context) {
		want := app.cfg.AutomationAPIToken
		got, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || want == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) != 1 {
			login(c)
			return
		}
		uid := common.SanitizeUid(app.cfg.AutomationAPIUser)
		user, err := app.userStorer.GetUser(uid)
		if err != nil || user == nil {
			log.Warn(autoOrganizeLogger, "API token user ", uid, " not found: ", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "the automation API token's user does not exist"})
			return
		}
		c.Set(userIDContextKey, uid)
		c.Set(backendVersionKey, common.Sync10)
		if user.Sync15 {
			c.Set(backendVersionKey, common.Sync15)
		}
		c.Next()
	}
}

func serveAutomationOpenAPI(c *gin.Context) {
	c.Data(http.StatusOK, "application/json", automationOpenAPI)
}

// maxSaveAttempts a rule edit reloads and retries when the config changed
// under it (another tab, another agent) at most this often
const maxSaveAttempts = 3

// errNoRule the rule id is not in the config
var errNoRule = errors.New("no rule with this id")

// editConfig loads the config, applies edit and saves it under the loaded
// revision, retrying on a concurrent change. edit returns errNoRule (404) or a
// *requestError (400) to refuse. The saved config is returned.
func (app *ReactAppWrapper) editConfig(c *gin.Context, edit func(cfg *automation.Config) error) (*automation.Config, bool) {
	uid := userID(c)
	store := app.organizer.Store()
	for attempt := 1; ; attempt++ {
		cfg, err := store.LoadConfig(uid)
		if err != nil {
			app.autoOrganizeError(c, err)
			return nil, false
		}
		if err := edit(cfg); err != nil {
			var re *requestError
			switch {
			case errors.Is(err, errNoRule):
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": err.Error()})
			case errors.As(err, &re):
				badReq(c, re.msg)
			default:
				app.autoOrganizeError(c, err)
			}
			return nil, false
		}
		saved, err := store.SaveConfig(uid, cfg)
		if errors.Is(err, autoorganize.ErrStaleRevision) && attempt < maxSaveAttempts {
			continue
		}
		if errors.Is(err, autoorganize.ErrStaleRevision) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error(), "revision": saved.Revision})
			return nil, false
		}
		var verr *automation.ValidationError
		if errors.As(err, &verr) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": verr.Error(), "ruleIndex": verr.RuleIndex, "ruleId": verr.RuleID})
			return nil, false
		}
		if err != nil {
			app.autoOrganizeError(c, err)
			return nil, false
		}
		log.Info(autoOrganizeLogger, uid, ": config saved through the rules API, revision ", saved.Revision)
		app.organizer.ConfigChanged(uid)
		return saved, true
	}
}

// requestError a refusal caused by the request (400)
type requestError struct{ msg string }

func (e *requestError) Error() string { return e.msg }

func ruleIndex(cfg *automation.Config, id string) int {
	for i := range cfg.Rules {
		if cfg.Rules[i].ID == id {
			return i
		}
	}
	return -1
}

// bindRule reads one rule; unknown fields are refused, so a typo is an error
// rather than a silently dropped setting
func bindRule(c *gin.Context) (*automation.Rule, error) {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var r automation.Rule
	if err := dec.Decode(&r); err != nil {
		return nil, &requestError{"invalid rule JSON: " + err.Error()}
	}
	return &r, nil
}

func (app *ReactAppWrapper) listRules(c *gin.Context) {
	cfg, err := app.organizer.Store().LoadConfig(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": cfg.Revision, "rules": cfg.Rules})
}

func (app *ReactAppWrapper) getRule(c *gin.Context) {
	cfg, err := app.organizer.Store().LoadConfig(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	i := ruleIndex(cfg, c.Param("ruleId"))
	if i < 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": errNoRule.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": cfg.Revision, "rule": cfg.Rules[i]})
}

// createRule POST rules: appended, or inserted at ?position= (1-based). The
// server assigns the id.
func (app *ReactAppWrapper) createRule(c *gin.Context) {
	r, err := bindRule(c)
	if err != nil {
		badReq(c, err.Error())
		return
	}
	if r.ID != "" {
		badReq(c, "leave id empty: the server assigns it")
		return
	}
	pos := 0
	if p := c.Query("position"); p != "" {
		if pos, err = strconv.Atoi(p); err != nil || pos < 1 {
			badReq(c, "position must be a whole number from 1")
			return
		}
	}
	var id string
	saved, ok := app.editConfig(c, func(cfg *automation.Config) error {
		nr := *r
		at := len(cfg.Rules)
		if pos > 0 && pos-1 < at {
			at = pos - 1
		}
		cfg.Rules = append(cfg.Rules[:at], append([]automation.Rule{nr}, cfg.Rules[at:]...)...)
		// Normalize gives the new rule its id and keeps the order
		cfg.Normalize()
		id = cfg.Rules[at].ID
		return nil
	})
	if !ok {
		return
	}
	i := ruleIndex(saved, id)
	c.JSON(http.StatusCreated, gin.H{"revision": saved.Revision, "rule": saved.Rules[i], "position": i + 1})
}

// replaceRule PUT rules/:ruleId: the rule as sent, keeping its id and position
func (app *ReactAppWrapper) replaceRule(c *gin.Context) {
	r, err := bindRule(c)
	if err != nil {
		badReq(c, err.Error())
		return
	}
	id := c.Param("ruleId")
	if r.ID != "" && r.ID != id {
		badReq(c, "the body's id differs from the path's; leave it out or send the same id")
		return
	}
	saved, ok := app.editConfig(c, func(cfg *automation.Config) error {
		i := ruleIndex(cfg, id)
		if i < 0 {
			return errNoRule
		}
		nr := *r
		nr.ID = id
		cfg.Rules[i] = nr
		return nil
	})
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": saved.Revision, "rule": saved.Rules[ruleIndex(saved, id)]})
}

// rulePatch the fields PATCH changes; everything else stays
type rulePatch struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

func (app *ReactAppWrapper) patchRule(c *gin.Context) {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var p rulePatch
	if err := dec.Decode(&p); err != nil {
		badReq(c, "invalid patch JSON (only name and enabled): "+err.Error())
		return
	}
	id := c.Param("ruleId")
	saved, ok := app.editConfig(c, func(cfg *automation.Config) error {
		i := ruleIndex(cfg, id)
		if i < 0 {
			return errNoRule
		}
		if p.Name != nil {
			cfg.Rules[i].Name = strings.TrimSpace(*p.Name)
		}
		if p.Enabled != nil {
			cfg.Rules[i].Enabled = *p.Enabled
		}
		return nil
	})
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": saved.Revision, "rule": saved.Rules[ruleIndex(saved, id)]})
}

func (app *ReactAppWrapper) deleteRule(c *gin.Context) {
	id := c.Param("ruleId")
	saved, ok := app.editConfig(c, func(cfg *automation.Config) error {
		i := ruleIndex(cfg, id)
		if i < 0 {
			return errNoRule
		}
		cfg.Rules = append(cfg.Rules[:i], cfg.Rules[i+1:]...)
		return nil
	})
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"revision": saved.Revision, "deleted": id})
}

// catalogueEntry one trigger, condition or action an agent can use
type catalogueEntry struct {
	Type          string         `json:"type"`
	Description   string         `json:"description"`
	Subjects      []string       `json:"subjects"`
	Fields        []string       `json:"fields"`
	Defaults      map[string]any `json:"defaults"`
	PageBased     bool           `json:"pageBased,omitempty"`
	Repeatable    bool           `json:"repeatable,omitempty"`
	BeforeTrigger bool           `json:"beforeTrigger,omitempty"`
}

// catalogueDocs one sentence per type, for agents (the UI has its own words)
var catalogueDocs = map[string]string{
	"trigger:" + automation.TriggerFinishedReading: "Read to the last page of a PDF or EPUB with at least minPages pages, then not opened for settleHours.",
	"trigger:" + automation.TriggerReadProgress:    "At least percent of a PDF or EPUB read, then not opened for settleHours.",
	"trigger:" + automation.TriggerNotOpened:       "Never opened, or last opened at least days ago.",
	"trigger:" + automation.TriggerNotModified:     "Last modified at least days ago (annotations count as edits).",
	"trigger:" + automation.TriggerNameMatches:     "Name matches pattern: a case-insensitive glob (* and ?) or /RE2 regex/.",
	"trigger:" + automation.TriggerTagIs:           "The document or any of its pages carries tag (case, spaces, - and _ ignored); a page tag lets a written #tag fire a document rule.",
	"trigger:" + automation.TriggerAgeSinceCreated: "Created at least days ago (created time, else last modified, else first seen by the server).",
	"trigger:" + automation.TriggerPinnedIs:        "Pinned (pinned true) or not pinned (false).",
	"trigger:" + automation.TriggerCreatedTime:     "Created in the last (compare within) or more than (older) amount unit ago; unit hours, days or weeks. Documents without a created time are skipped.",
	"trigger:" + automation.TriggerModifiedTime:    "Last changed in the last (within) or more than (older) amount unit ago; unit hours, days or weeks.",
	"trigger:" + automation.TriggerPageChanged:     "A page got new or changed handwriting (page rules).",
	"trigger:" + automation.TriggerPageTagged:      "A page carries the tag tag, as a page tag or as a tag of its document (page rules). A document tag is the same as tagging every page.",
	"condition:" + automation.CondKindIs:           "Only notebooks (kind notebooks) or only PDFs and EPUBs (pdf_epub).",
	"condition:" + automation.CondTagIs:            "The document carries tag.",
	"condition:" + automation.CondTagIsNot:         "The document does not carry tag.",
	"condition:" + automation.CondNotOpened:        "Never opened, or last opened at least days ago.",
	"condition:" + automation.CondMinAgeDays:       "At least days old (0 allowed).",
	"condition:" + automation.CondNameMatches:      "Name matches pattern (glob or /regex/).",
	"condition:" + automation.CondPinnedIs:         "Pinned or not, per pinned.",
	"condition:" + automation.CondPageTagIs:        "The page carries the page tag tag.",
	"condition:" + automation.CondCreatedTime:      "Created in the last / more than amount unit ago (of the page's document on a page rule). Repeatable, for a window.",
	"condition:" + automation.CondModifiedTime:     "Changed in the last / more than amount unit ago (of the page's document on a page rule). Repeatable, for a window.",
	"action:" + automation.ActionMove:              "Move the document to the folder in to: {id} of an existing folder (\"\" = My files), or {createIfMissing: {name, parent: \"source\" or a folder id}}.",
	"action:" + automation.ActionAddTag:            "Add the document tag tag (written to the document; the tablet gets it on its next sync).",
	"action:" + automation.ActionRemoveTag:         "Remove the document tag tag.",
	"action:" + automation.ActionExtractTasks:      "Send the changed page to rm-pages: save it and its annotations (highlights with context, margin notes, crops) to its vault note and add new to-dos to the inbox (paid LLM call per page with handwriting). prompt is optional; model is ignored (rm-pages uses its flash profile).",
	"action:" + automation.ActionSaveNotes:         "Send the changed page to rm-pages: save it and its annotations (highlights with context, margin notes, crops) to its vault note, no tasks (paid LLM call per page with handwriting).",
}

// catalogueActions the actions, with the subject each works on and its fields
var catalogueActions = []struct {
	typ     string
	subject string
	fields  []string
}{
	{automation.ActionMove, automation.SubjectDocument, []string{"to"}},
	{automation.ActionAddTag, automation.SubjectDocument, []string{"tag"}},
	{automation.ActionRemoveTag, automation.SubjectDocument, []string{"tag"}},
	{automation.ActionExtractTasks, automation.SubjectPage, []string{"prompt", "model"}},
	{automation.ActionSaveNotes, automation.SubjectPage, []string{"prompt", "model"}},
}

func specEntries(kind string, specs map[string]*automation.Spec) []catalogueEntry {
	out := make([]catalogueEntry, 0, len(specs))
	for typ, s := range specs {
		p := automation.Params{}
		if s.Defaults != nil {
			s.Defaults(&p)
		}
		var all map[string]any
		b, _ := json.Marshal(p)
		_ = json.Unmarshal(b, &all)
		defs := map[string]any{}
		for _, f := range s.Fields {
			defs[f] = all[f]
		}
		out = append(out, catalogueEntry{Type: typ, Description: catalogueDocs[kind+":"+typ],
			Subjects: s.Subjects, Fields: append([]string{}, s.Fields...), Defaults: defs,
			PageBased: s.PageBased, Repeatable: s.Repeatable, BeforeTrigger: s.BeforeTrigger})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// automationCatalogue GET catalogue: every trigger, condition and action
func automationCatalogue(c *gin.Context) {
	actions := make([]catalogueEntry, 0, len(catalogueActions))
	for _, a := range catalogueActions {
		actions = append(actions, catalogueEntry{Type: a.typ, Description: catalogueDocs["action:"+a.typ],
			Subjects: []string{a.subject}, Fields: a.fields, Defaults: map[string]any{}})
	}
	c.JSON(http.StatusOK, gin.H{
		"triggers":   specEntries("trigger", automation.Triggers),
		"conditions": specEntries("condition", automation.Conditions),
		"actions":    actions,
	})
}
