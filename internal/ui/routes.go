package ui

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// RegisterRoutes the apps routes
func (app *ReactAppWrapper) RegisterRoutes(router *gin.Engine) {
	router.StaticFS(app.prefix, app)

	router.GET("/favicon.ico", func(c *gin.Context) {
		c.FileFromFS("/favicon.ico", app.fs)
	})
	router.GET("/robots.txt", func(c *gin.Context) {
		c.FileFromFS("/robots.txt", app.fs)
	})
	// the automation API's OpenAPI description; no auth (it holds no data)
	router.GET("/openapi.json", serveAutomationOpenAPI)
	// the tablet's "#" pane: what each rule tag does, for AUTOMATION_API_USER;
	// no auth (tailnet only; it holds tag and folder names, no documents)
	router.GET("/tag-help", app.tagHelp)

	//hack for index.html
	router.NoRoute(func(c *gin.Context) {
		uri := c.Request.RequestURI
		log.Info(uri)
		if strings.HasPrefix(uri, "/api") ||
			strings.HasPrefix(uri, "/ui/api") ||
			c.Request.Method != http.MethodGet {

			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		c.FileFromFS(indexReplacement, app)
	})

	r := router.Group("/ui/api")
	r.POST("register", app.register)
	r.POST("login", app.login)
	// Error reporting config for the browser. No auth: errors on the login page
	// count too, and a DSN only grants sending events.
	r.GET("glitchtip", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"dsn": app.cfg.GlitchtipBrowserDSN, "release": app.cfg.Version})
	})
	r.GET("logout", func(c *gin.Context) {
		c.SetCookie(cookieName, "/", -1, "", "", false, true)
		c.Status(http.StatusOK)
	})
	//with authentication
	auth := r.Group("")
	auth.Use(app.authMiddleware())
	auth.HEAD("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	auth.GET("sync", func(c *gin.Context) {
		uid := userID(c)
		br := c.GetString(browserIDContextKey)
		log.Info("browser", br)
		app.h.NotifySync(uid, br)
	})

	auth.GET("newcode", app.newCode)

	// passcode (PIN) reset approval
	auth.GET("passcode/resets", app.listPasscodeResets)
	auth.POST("passcode/resets/:uuid/approve", app.approvePasscodeReset)
	auth.DELETE("passcode/resets/:uuid", app.dismissPasscodeReset)

	// auth.GET("profile", app.newCode)
	auth.POST("profile", app.changePassword)
	// auth.POST("changeEmail", app.changePassword)

	auth.GET("documents", app.listDocuments)
	auth.GET("documents/:docid", app.getDocument)
	auth.POST("documents/upload", app.createDocument)

	//move, rename
	auth.DELETE("documents/:docid", app.deleteDocument)
	auth.PUT("documents", app.updateDocument)
	auth.POST("folders", app.createFolder)
	auth.GET("documents/:docid/metadata", app.getDocumentMetadata)
	app.registerDocTagsRoute(auth)

	// integrations
	auth.GET("integrations", app.listIntegrations)
	auth.POST("integrations", app.createIntegration)
	auth.GET("integrations/:intid", app.getIntegration)
	auth.PUT("integrations/:intid", app.updateIntegration)
	auth.DELETE("integrations/:intid", app.deleteIntegration)

	auth.GET("integrations/:intid/explore/*path", app.exploreIntegration)
	auth.GET("integrations/:intid/metadata/*path", app.getMetadataIntegration)
	auth.GET("integrations/:intid/download/*path", app.downloadThroughIntegration)

	app.registerAutomationRoutes(r, auth)

	ss := auth.Group("screenshare")
	ss.GET("room", app.screenshareJoinActive)
	ss.GET("room/:roomId", app.screenshareGetRoom)
	ss.GET("offer", app.screenshareGetOffer)
	ss.POST("room/:roomId/answer", app.screenshareSendAnswer)
	ss.DELETE("room/:roomId", app.screenshareDeleteRoom)

	//admin
	admin := auth.Group("")
	admin.Use(app.adminMiddleware())
	admin.GET("users/:userid", app.getUser)
	admin.DELETE("users/:userid", app.deleteUser)
	admin.PUT("users", app.updateUser)
	admin.POST("users", app.createUser)
	admin.GET("users", app.getAppUsers)
}
