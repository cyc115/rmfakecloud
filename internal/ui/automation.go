package ui

import (
	"errors"
	"net/http"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/pagewatch"
	"github.com/ddvk/rmfakecloud/internal/ui/viewmodel"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const autoOrganizeLogger = "[automation] "

// registerAutomationRoutes the rules API (R-B6). The old auto-organize paths
// answer 410 with a pointer; they are registered first, outside the sync15
// check, and do no work.
//
// The automation group takes the web UI's login or the AUTOMATION_API_TOKEN
// bearer (automationAuth); its OpenAPI description is GET /openapi.json.
func (app *ReactAppWrapper) registerAutomationRoutes(api, auth *gin.RouterGroup) {
	auth.Any("auto-organize", movedToAutomation)
	auth.Any("auto-organize/*rest", movedToAutomation)

	ao := api.Group("automation")
	ao.Use(app.automationAuth(), app.requireSync15())
	ao.GET("catalogue", automationCatalogue)
	ao.GET("rules", app.listRules)
	ao.POST("rules", app.createRule)
	ao.GET("rules/:ruleId", app.getRule)
	ao.PUT("rules/:ruleId", app.replaceRule)
	ao.PATCH("rules/:ruleId", app.patchRule)
	ao.DELETE("rules/:ruleId", app.deleteRule)
	ao.GET("", app.getAutomation)
	ao.PUT("", app.putAutomation)
	ao.GET("state", app.autoOrganizeState)
	ao.POST("preview", app.autoOrganizePreview)
	ao.POST("run-now", app.autoOrganizeRunNow)
	ao.GET("watchables", app.autoOrganizeWatchables)
	ao.GET("page-tags", app.automationPageTags)
	ao.POST("page-tags/apply", app.applyPageTags)
	ao.GET("pipeline", app.pipelineStatus)
	ao.POST("pipeline/run-now", app.pipelineRunNow)
	ao.GET("log", app.autoOrganizeLog)
	ao.GET("runs", app.autoOrganizeLog)
	ao.POST("undo/:entryId", app.autoOrganizeUndo)
	ao.POST("reapply/:holdId", app.autoOrganizeReapply)
}

// movedToAutomation the old /ui/api/auto-organize* API: 410 and where it went.
// It reads no body and touches no file, so an old tab's PUT changes nothing.
func movedToAutomation(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusGone, gin.H{
		"error":    "this API moved to /ui/api/automation",
		"location": "/ui/api/automation" + c.Param("rest"),
	})
}

// requireSync15 the automation endpoints refuse sync10 sessions
func (app *ReactAppWrapper) requireSync15() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, ok := c.Get(backendVersionKey); !ok || v != common.Sync15 {
			badReq(c, autoorganize.ErrSync10.Error())
			return
		}
		if app.organizer == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, viewmodel.NewErrorResponse("automation is not available"))
			return
		}
		c.Next()
	}
}

func (app *ReactAppWrapper) autoOrganizeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, autoorganize.ErrSync10):
		badReq(c, err.Error())
	case errors.Is(err, autoorganize.ErrBusy):
		c.AbortWithStatusJSON(http.StatusConflict, viewmodel.NewErrorResponse(err.Error()))
	case errors.Is(err, autoorganize.ErrPaused), errors.Is(err, autoorganize.ErrAlreadyUndone):
		c.AbortWithStatusJSON(http.StatusConflict, viewmodel.NewErrorResponse(err.Error()))
	case errors.Is(err, autoorganize.ErrNoEntry):
		c.AbortWithStatusJSON(http.StatusNotFound, viewmodel.NewErrorResponse(err.Error()))
	case autoorganize.IsNotQualified(err):
		c.AbortWithStatusJSON(http.StatusConflict, viewmodel.NewErrorResponse(err.Error()))
	default:
		log.Error(autoOrganizeLogger, err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, viewmodel.NewErrorResponse(err.Error()))
	}
}

func (app *ReactAppWrapper) getAutomation(c *gin.Context) {
	cfg, err := app.organizer.Store().LoadConfig(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (app *ReactAppWrapper) putAutomation(c *gin.Context) {
	uid := userID(c)
	var cfg automation.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		badReq(c, err.Error())
		return
	}
	saved, err := app.organizer.Store().SaveConfig(uid, &cfg)
	if errors.Is(err, autoorganize.ErrStaleRevision) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error(), "revision": saved.Revision})
		return
	}
	var verr *automation.ValidationError
	if errors.As(err, &verr) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": verr.Error(), "ruleIndex": verr.RuleIndex, "ruleId": verr.RuleID})
		return
	}
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	log.Info(autoOrganizeLogger, uid, ": config saved, revision ", saved.Revision)
	app.organizer.ConfigChanged(uid)
	c.JSON(http.StatusOK, saved)
}

func (app *ReactAppWrapper) autoOrganizeState(c *gin.Context) {
	view, err := app.organizer.View(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// previewRequest all rules, one rule, or a draft rule body
type previewRequest struct {
	RuleID string           `json:"ruleId"`
	Rule   *automation.Rule `json:"rule"`
}

// autoOrganizePreview POST preview: a document rule (or none) answers the
// sweep's Plan; a page rule answers a PagePreview of what it covers now.
func (app *ReactAppWrapper) autoOrganizePreview(c *gin.Context) {
	var req previewRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badReq(c, err.Error())
			return
		}
	}
	uid := userID(c)
	if page := app.pageRuleOf(c, uid, &req); c.IsAborted() {
		return
	} else if page != nil {
		app.previewPageRule(c, uid, page)
		return
	}
	plan, _, err := app.organizer.Preview(uid, req.Rule)
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	only := req.RuleID
	if req.Rule != nil {
		only = req.Rule.ID
	}
	if only != "" {
		plan = filterPlan(plan, only)
	}
	c.JSON(http.StatusOK, plan)
}

// pageRuleOf the page rule a preview request names: the draft, or the saved
// rule with ruleId; nil for a document rule, no rule or an unknown id.
func (app *ReactAppWrapper) pageRuleOf(c *gin.Context, uid string, req *previewRequest) *automation.Rule {
	if req.Rule != nil {
		if !req.Rule.IsPage() {
			return nil
		}
		// the same normalisation the store applies on save
		tmp := &automation.Config{Rules: []automation.Rule{*req.Rule}}
		tmp.Normalize()
		return &tmp.Rules[0]
	}
	if req.RuleID == "" {
		return nil
	}
	cfg, err := app.organizer.Store().LoadConfig(uid)
	if err != nil {
		app.autoOrganizeError(c, err)
		return nil
	}
	for i := range cfg.Rules {
		if cfg.Rules[i].ID == req.RuleID && cfg.Rules[i].IsPage() {
			return &cfg.Rules[i]
		}
	}
	return nil
}

func (app *ReactAppWrapper) previewPageRule(c *gin.Context, uid string, r *automation.Rule) {
	if app.blobs == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, viewmodel.NewErrorResponse("page preview is not available"))
		return
	}
	snap, content, err := pageSnapshot(app.blobs, uid)
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, pagewatch.PreviewRule(snap, content, r))
}

func filterPlan(p *autoorganize.Plan, ruleID string) *autoorganize.Plan {
	out := &autoorganize.Plan{At: p.At, Rules: []autoorganize.RuleResult{}, Moves: []autoorganize.PlannedMove{}, Skipped: []autoorganize.Skip{}}
	for _, r := range p.Rules {
		if r.RuleID == ruleID {
			out.Rules = append(out.Rules, r)
			out.Scanned = r.Checked
		}
	}
	for _, m := range p.Moves {
		if m.RuleID == ruleID {
			out.Moves = append(out.Moves, m)
		}
	}
	for _, s := range p.Skipped {
		if s.RuleID == ruleID {
			out.Skipped = append(out.Skipped, s)
		}
	}
	return out
}

func (app *ReactAppWrapper) autoOrganizeRunNow(c *gin.Context) {
	res, err := app.organizer.RunNow(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (app *ReactAppWrapper) autoOrganizeLog(c *gin.Context) {
	l, err := app.organizer.Log(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	switch c.Query("tab") {
	case "moves":
		c.JSON(http.StatusOK, gin.H{"moves": l.Moves})
	case "runs":
		c.JSON(http.StatusOK, gin.H{"runs": l.Runs})
	default:
		c.JSON(http.StatusOK, l)
	}
}

func (app *ReactAppWrapper) autoOrganizeUndo(c *gin.Context) {
	entry, err := app.organizer.Undo(userID(c), c.Param("entryId"))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (app *ReactAppWrapper) autoOrganizeReapply(c *gin.Context) {
	entry, err := app.organizer.Reapply(userID(c), c.Param("holdId"))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (app *ReactAppWrapper) autoOrganizeWatchables(c *gin.Context) {
	items, err := app.organizer.Watchables(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// maxPipelineRuns runs shown in the Pipeline panel
const maxPipelineRuns = 20

// pipelineStatus the Pipeline panel: page watch status, the last runs and the
// rule drawer's defaults. With page watch off it still answers, so the drawer
// can prefill; only run-now needs the service.
func (app *ReactAppWrapper) pipelineStatus(c *gin.Context) {
	out := gin.H{
		"enabled":         false,
		"lastRun":         nil,
		"nextAt":          0,
		"pendingPipeline": 0,
		"lastError":       "",
		"missingWatches":  []pagewatch.MissingWatch{},
		"runs":            []pagewatch.RunEntry{},
		"defaults": gin.H{
			"prompt": autoorganize.DefaultPipelinePrompt,
			"model":  autoorganize.DefaultPipelineModel,
		},
	}
	if app.pageWatch != nil {
		uid := userID(c)
		st, err := app.pageWatch.Status(uid)
		if err != nil {
			app.pipelineError(c, err)
			return
		}
		runs, err := app.pageWatch.Store().LoadRuns(uid)
		if err != nil {
			app.pipelineError(c, err)
			return
		}
		if len(runs) > maxPipelineRuns {
			runs = runs[:maxPipelineRuns]
		}
		out["enabled"] = true
		out["lastRun"] = st.LastRun
		out["nextAt"] = st.NextAt
		out["pendingPipeline"] = st.PendingPipeline
		out["lastError"] = st.LastError
		out["missingWatches"] = st.MissingWatches
		out["runs"] = runs
	}
	c.JSON(http.StatusOK, out)
}

func (app *ReactAppWrapper) pipelineRunNow(c *gin.Context) {
	if app.pageWatch == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, viewmodel.NewErrorResponse("page watch is not available"))
		return
	}
	run, err := app.pageWatch.RunNow(userID(c))
	if err != nil {
		app.pipelineError(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

func (app *ReactAppWrapper) pipelineError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, pagewatch.ErrBusy):
		c.AbortWithStatusJSON(http.StatusConflict, viewmodel.NewErrorResponse(err.Error()))
	case errors.Is(err, pagewatch.ErrSync10):
		badReq(c, err.Error())
	default:
		log.Error(autoOrganizeLogger, err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, viewmodel.NewErrorResponse(err.Error()))
	}
}
