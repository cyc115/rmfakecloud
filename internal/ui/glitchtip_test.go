package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/gin-gonic/gin"
)

// The browser fetches its error reporting config before anyone logs in.
func TestGlitchtipConfigIsPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{GlitchtipDSN: "http://key@host.containers.internal:8797/7", GlitchtipBrowserDSN: "http://key@log.svc/7", Version: "1.2.3", JWTSecretKey: []byte("x")}
	app := &ReactAppWrapper{cfg: cfg, prefix: "/assets"}
	router := gin.New()
	app.RegisterRoutes(router)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/ui/api/glitchtip", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["dsn"] != cfg.GlitchtipBrowserDSN || body["release"] != "1.2.3" {
		t.Errorf("body %v", body)
	}
}
