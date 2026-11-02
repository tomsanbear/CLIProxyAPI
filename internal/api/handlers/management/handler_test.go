package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"golang.org/x/crypto/bcrypt"
)

func newHashedSecretHandler(t *testing.T, secret string) *Handler {
	t.Helper()
	hash, errHash := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if errHash != nil {
		t.Fatalf("hash secret: %v", errHash)
	}
	cfg := &config.Config{}
	cfg.RemoteManagement.SecretKey = string(hash)
	return &Handler{cfg: cfg, failedAttempts: make(map[string]*attemptInfo)}
}

func TestAuthenticateManagementKey_CachesVerifiedKey(t *testing.T) {
	h := newHashedSecretHandler(t, "panel-secret")
	secretHash := h.cfg.RemoteManagement.SecretKey

	if h.verifiedKeys.matches(secretHash, "panel-secret") {
		t.Fatalf("key must not be cached before it is verified")
	}
	for i := 0; i < 3; i++ {
		if allowed, status, msg := h.AuthenticateManagementKey("127.0.0.1", true, "panel-secret"); !allowed {
			t.Fatalf("attempt %d: expected valid key to pass, got %d %q", i+1, status, msg)
		}
	}
	if !h.verifiedKeys.matches(secretHash, "panel-secret") {
		t.Fatalf("expected verified key to be cached")
	}
	if h.verifiedKeys.matches(secretHash, "other-key") {
		t.Fatalf("cache must not accept a different key")
	}
}

func TestAuthenticateManagementKey_CachedKeyInvalidatedBySecretRotation(t *testing.T) {
	h := newHashedSecretHandler(t, "old-secret")
	if allowed, _, _ := h.AuthenticateManagementKey("127.0.0.1", true, "old-secret"); !allowed {
		t.Fatalf("expected old secret to pass before rotation")
	}

	rotated := newHashedSecretHandler(t, "new-secret")
	h.cfg = rotated.cfg
	if allowed, status, _ := h.AuthenticateManagementKey("127.0.0.1", true, "old-secret"); allowed ||
		status != http.StatusUnauthorized {
		t.Fatalf("expected old secret to be rejected after rotation, got allowed=%v status=%d", allowed, status)
	}
	if allowed, _, _ := h.AuthenticateManagementKey("127.0.0.1", true, "new-secret"); !allowed {
		t.Fatalf("expected new secret to pass after rotation")
	}
}

func TestAuthenticateManagementKey_WrongKeysStillBanWithCachedKey(t *testing.T) {
	h := newHashedSecretHandler(t, "panel-secret")
	if allowed, _, _ := h.AuthenticateManagementKey("127.0.0.1", true, "panel-secret"); !allowed {
		t.Fatalf("expected valid key to pass")
	}
	for i := 0; i < 5; i++ {
		if allowed, _, _ := h.AuthenticateManagementKey("127.0.0.1", true, "guess"); allowed {
			t.Fatalf("expected wrong key to be rejected at attempt %d", i+1)
		}
	}
	allowed, status, msg := h.AuthenticateManagementKey("127.0.0.1", true, "panel-secret")
	if allowed || status != http.StatusForbidden || !strings.HasPrefix(msg, "IP banned") {
		t.Fatalf("expected ban to override the cached key, got allowed=%v status=%d msg=%q", allowed, status, msg)
	}
}

func TestAuthenticateManagementKey_LocalhostIPBan_BlocksCorrectKeyDuringBan(t *testing.T) {
	h := &Handler{
		cfg:            &config.Config{},
		failedAttempts: make(map[string]*attemptInfo),
		envSecret:      "test-secret",
	}

	for i := 0; i < 5; i++ {
		allowed, statusCode, errMsg := h.AuthenticateManagementKey("127.0.0.1", true, "wrong-secret")
		if allowed {
			t.Fatalf("expected auth to be denied at attempt %d", i+1)
		}
		if statusCode != http.StatusUnauthorized || errMsg != "invalid management key" {
			t.Fatalf("unexpected auth failure at attempt %d: status=%d msg=%q", i+1, statusCode, errMsg)
		}
	}

	allowed, statusCode, errMsg := h.AuthenticateManagementKey("127.0.0.1", true, "test-secret")
	if allowed {
		t.Fatalf("expected correct key to be denied while banned")
	}
	if statusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden status while banned, got %d", statusCode)
	}
	if !strings.HasPrefix(errMsg, "IP banned due to too many failed attempts. Try again in") {
		t.Fatalf("unexpected banned message: %q", errMsg)
	}
}

func TestMiddlewareSetsSupportPluginHeader(t *testing.T) {

	h := &Handler{
		cfg:            &config.Config{},
		failedAttempts: make(map[string]*attemptInfo),
		envSecret:      "test-secret",
	}
	middleware := h.Middleware()

	t.Run("invalid key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		c.Request.RemoteAddr = "127.0.0.1:12345"
		c.Request.Header.Set("X-Management-Key", "wrong-secret")

		middleware(c)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != pluginhost.SupportPluginHeaderValue() {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, pluginhost.SupportPluginHeaderValue())
		}
	})

	t.Run("valid key", func(t *testing.T) {
		engine := gin.New()
		engine.GET("/v0/management/config", middleware, func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Management-Key", "test-secret")
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != pluginhost.SupportPluginHeaderValue() {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, pluginhost.SupportPluginHeaderValue())
		}
	})
}
