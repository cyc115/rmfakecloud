package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/app/passcodestore"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/glitchtip"
	"github.com/ddvk/rmfakecloud/internal/hwr"
	"github.com/ddvk/rmfakecloud/internal/mqtt"
	"github.com/ddvk/rmfakecloud/internal/pagewatch"
	"github.com/ddvk/rmfakecloud/internal/screenshare"
	"github.com/ddvk/rmfakecloud/internal/storage"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/ui"

	"github.com/gin-gonic/gin"
)

const (
	userIDKey      = "UserID"
	deviceIDKey    = "DeviceID"
	syncVersionKey = "SyncVersion"
)

// App web app
type App struct {
	router        *gin.Engine
	cfg           *config.Config
	srv           *http.Server
	docStorer     storage.DocumentStorer
	userStorer    storage.UserStorer
	metaStorer    storage.MetadataStorer
	blobStorer    storage.BlobStorage
	hub           *hub.Hub
	passcodeStore passcodestore.Store
	codeConnector CodeConnector
	hwrClient     *hwr.HWRClient
	mqttBroker    *mqtt.Broker
	roomManager   *screenshare.RoomManager
	organizer     *autoorganize.Service
	stopOrganizer context.CancelFunc
	// pageWatch sends page changes to rm-pages; nil unless RM_PAGES_URL is set.
	// The page-watch HTTP handlers reach it through the App (not ui.New).
	pageWatch     *pagewatch.Service
	stopPageWatch context.CancelFunc
}

// PageWatch the page watch service, nil when RM_PAGES_URL is not set
func (app *App) PageWatch() *pagewatch.Service { return app.pageWatch }

// Start starts the app
func (app *App) Start() {
	// configs
	if app.cfg.CloudHost != config.DefaultHost {
		log.Info(config.EnvStorageURL, " (Cloud URL): ", app.cfg.StorageURL)
		log.Info("(Cloud HOST): ", app.cfg.CloudHost)
	}
	log.Info("Data: ", app.cfg.DataDir)
	log.Info("HTTP listening on port: ", app.cfg.Port)

	if !app.cfg.TrustProxy {
		app.router.SetTrustedProxies(nil)
	}

	var tlsConfig *tls.Config
	if app.cfg.Certificate.Certificate != nil {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{
				app.cfg.Certificate,
			},
		}
		log.Info("TLS certificates configured")
	}

	if app.mqttBroker != nil {
		log.Infof("MQTT listening on port: %s", app.cfg.MQTTPort)
		if tlsConfig != nil {
			app.mqttBroker.SetTLSConfig(tlsConfig)
		}
		if err := app.mqttBroker.Start(); err != nil {
			log.Errorf("Failed to start MQTT broker: %v", err)
		}
	}

	app.migrateAutomation()
	// the sweeper runs until Stop; it does not sweep until the user list is readable
	organizerCtx, stopOrganizer := context.WithCancel(context.Background())
	app.stopOrganizer = stopOrganizer
	app.organizer.Start(organizerCtx)
	if app.pageWatch != nil {
		pwCtx, stopPW := context.WithCancel(context.Background())
		app.stopPageWatch = stopPW
		app.pageWatch.Start(pwCtx)
	}

	app.srv = &http.Server{
		Addr:      ":" + app.cfg.Port,
		Handler:   app.router,
		TLSConfig: tlsConfig,
	}

	if tlsConfig != nil {
		log.Info("Starting HTTPS server")
		if err := app.srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	} else {
		log.Info("Starting HTTP server (plain)")
		if err := app.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}
}

// Stop the app
func (app *App) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// app.hub.Stop()
	if app.stopOrganizer != nil {
		app.stopOrganizer()
		// let an in-flight sweep record its state, within the shutdown budget
		done := make(chan struct{})
		go func() {
			app.organizer.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			log.Warn("auto-organize: sweeper did not stop in time")
		}
	}
	if app.stopPageWatch != nil {
		app.stopPageWatch()
		done := make(chan struct{})
		go func() {
			app.pageWatch.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			log.Warn("page-watch: watcher did not stop in time")
		}
	}
	if app.mqttBroker != nil {
		if err := app.mqttBroker.Stop(); err != nil {
			log.Errorf("Error stopping MQTT broker: %v", err)
		}
	}
	if err := app.srv.Shutdown(ctx); err != nil {
		log.Fatal("Server Shutdown:", err)
	}
}

// NewApp constructs an app
func NewApp(cfg *config.Config) App {
	debugMode := log.GetLevel() >= log.DebugLevel
	if !debugMode {
		gin.SetMode(gin.ReleaseMode)
	}

	fsStorage := fs.NewStorage(cfg)
	usrs, err := fsStorage.GetUsers()

	if err != nil {
		log.Warn(err)
	}

	if len(usrs) == 0 {
		log.Warn("No users found, the first login will create a user")
		//TODO: not thread safe
		cfg.CreateFirstUser = true
	}
	ntfHub := hub.NewHub()
	organizer := autoorganize.NewService(fsStorage, ntfHub, cfg.AutoOrganizeInterval)
	pcStore := passcodestore.NewInMemory()
	codeConnector := NewCodeConnector()
	router := gin.Default()
	// inside gin's Recovery: reports the panic, then lets Recovery answer 500
	router.Use(glitchtip.Default().GinRecovery())

	// corsConfig := cors.DefaultConfig()

	// // corsConfig.AllowOrigins = []string{"*"}

	// // To be able to send tokens to the server.
	// // corsConfig.AllowCredentials = true

	// // OPTIONS method for ReactJS
	// corsConfig.AddAllowMethods("OPTIONS")
	// corsConfig.AddAllowHeaders("Authorization")

	// Register the middleware
	// router.Use(cors.New(corsConfig))

	if debugMode {
		router.Use(requestLoggerMiddleware())
	}

	app := App{
		router:        router,
		cfg:           cfg,
		docStorer:     fsStorage,
		userStorer:    fsStorage,
		metaStorer:    fsStorage,
		blobStorer:    fsStorage,
		hub:           ntfHub,
		organizer:     organizer,
		passcodeStore: pcStore,
		codeConnector: codeConnector,
		hwrClient: &hwr.HWRClient{
			Cfg: cfg,
		},
	}

	if cfg.RMPagesURL != "" {
		pw := pagewatch.NewService(fsStorage, organizer.Store(), pagewatch.NewHTTPSender(cfg.RMPagesURL, nil))
		pw.MinAge = cfg.PageWatchMinAge
		app.pageWatch = pw
	}

	roomMgr := screenshare.NewRoomManager()
	app.roomManager = roomMgr
	app.mqttBroker = mqtt.NewBroker(cfg.MQTTPort, nil, app.validateMQTTToken, cfg.ICEServers, roomMgr, ntfHub)

	app.registerRoutes(router)

	uiApp := ui.New(cfg, fsStorage, codeConnector, ntfHub, pcStore, fsStorage, fsStorage, roomMgr, app.mqttBroker, organizer, app.pageWatch)
	uiApp.RegisterRoutes(router)

	storageapp := fs.NewApp(cfg, fsStorage)
	storageapp.OnRootUpdated = organizer.Trigger
	storageapp.RegisterRoutes(router)

	return app
}

// migrateAutomation migrates every user's auto-organize.json to
// automation.json before the engines start. A failure is logged and does not
// stop the server: that user's engines fail closed until it is fixed.
func (app *App) migrateAutomation() {
	users, err := app.userStorer.GetUsers()
	if err != nil {
		log.Error("automation: cannot list users for the config migration: ", err)
		return
	}
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	for uid, err := range app.organizer.Store().MigrateAll(ids) {
		log.Errorf("automation: %s: config not migrated: %v", uid, err)
	}
}

func badReq(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": message})
}

func internalError(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": message})
}

func (app *App) validateMQTTToken(token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("empty token")
	}

	claims := &UserClaims{}
	err := common.ClaimsFromToken(claims, token, app.cfg.JWTSecretKey)
	if err == nil && claims.Profile.UserID != "" && claims.Version == tokenVersion {
		userID := common.SanitizeUid(strings.TrimPrefix(claims.Profile.UserID, "auth0|"))
		return userID, nil
	}

	webClaims := &ui.WebUserClaims{}
	err = common.ClaimsFromToken(webClaims, token, app.cfg.JWTSecretKey)
	if err == nil && webClaims.UserID != "" {
		return common.SanitizeUid(webClaims.UserID), nil
	}

	return "", fmt.Errorf("invalid token")
}
