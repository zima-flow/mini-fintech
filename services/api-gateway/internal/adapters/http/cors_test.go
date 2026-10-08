package gatewayhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func corsProbe(cfg CORSConfig, reached *bool) http.Handler {
	return CORS(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCORS_Preflight(t *testing.T) {
	t.Parallel()

	reached := false
	req := httptest.NewRequest(http.MethodOptions, "/v1/auth/login", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	corsProbe(CORSConfig{AllowedOrigins: []string{"https://app.example"}}, &reached).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "https://app.example", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key")
	require.False(t, reached)
}

func TestCORS_EchoesConfiguredOrigin(t *testing.T) {
	t.Parallel()

	reached := false
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	corsProbe(CORSConfig{AllowedOrigins: []string{"https://app.example"}}, &reached).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, reached)
	require.Equal(t, "https://app.example", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	t.Parallel()

	reached := false
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	corsProbe(CORSConfig{AllowedOrigins: []string{"https://app.example"}}, &reached).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, reached)
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_NoOriginPassesThrough(t *testing.T) {
	t.Parallel()

	reached := false
	rec := httptest.NewRecorder()
	corsProbe(CORSConfig{AllowedOrigins: []string{"https://app.example"}}, &reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, reached)
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_AllowsAnyWhenUnconfigured(t *testing.T) {
	t.Parallel()

	reached := false
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Origin", "https://any.example")
	rec := httptest.NewRecorder()
	corsProbe(CORSConfig{}, &reached).ServeHTTP(rec, req)

	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.True(t, reached)
}
