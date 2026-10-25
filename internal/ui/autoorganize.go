package ui

import (
	"errors"
	"net/http"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/ui/viewmodel"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const autoOrganizeLogger = "[auto-organize] "

func (app *ReactAppWrapper) registerAutoOrganizeRoutes(auth *gin.RouterGroup) {
	ao := auth.Group("auto-organize")
	ao.Use(app.requireSync15())
	ao.GET("", app.getAutoOrganize)
	ao.PUT("", app.putAutoOrganize)
	ao.GET("state", app.autoOrganizeState)
	ao.POST("preview", app.autoOrganizePreview)
	ao.POST("run-now", app.autoOrganizeRunNow)
	ao.GET("log", app.autoOrganizeLog)
	ao.GET("runs", app.autoOrganizeLog)
	ao.POST("undo/:entryId", app.autoOrganizeUndo)
	ao.POST("reapply/:holdId", app.autoOrganizeReapply)
}

// requireSync15 the auto-organize endpoints refuse sync10 sessions
func (app *ReactAppWrapper) requireSync15() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, ok := c.Get(backendVersionKey); !ok || v != common.Sync15 {
			badReq(c, autoorganize.ErrSync10.Error())
			return
		}
		if app.organizer == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, viewmodel.NewErrorResponse("auto-organize is not available"))
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

func (app *ReactAppWrapper) getAutoOrganize(c *gin.Context) {
	cfg, err := app.organizer.Store().LoadConfig(userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (app *ReactAppWrapper) putAutoOrganize(c *gin.Context) {
	uid := userID(c)
	var cfg autoorganize.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		badReq(c, err.Error())
		return
	}
	saved, err := app.organizer.Store().SaveConfig(uid, &cfg)
	if errors.Is(err, autoorganize.ErrStaleRevision) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error(), "revision": saved.Revision})
		return
	}
	var verr *autoorganize.ValidationError
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
	RuleID string             `json:"ruleId"`
	Rule   *autoorganize.Rule `json:"rule"`
}

func (app *ReactAppWrapper) autoOrganizePreview(c *gin.Context) {
	var req previewRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badReq(c, err.Error())
			return
		}
	}
	plan, _, err := app.organizer.Preview(userID(c), req.Rule)
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
