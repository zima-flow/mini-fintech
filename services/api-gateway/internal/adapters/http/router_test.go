package gatewayhttp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestRouter(handler http.Handler) http.Handler {
	return NewRouter(Config{Logger: discardLogger(), RequestID: id.UUIDv7{}}, handler)
}

func TestRouter_EchoesIncomingRequestID(t *testing.T) {
	t.Parallel()

	var seen string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = interceptors.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "caller-supplied")
	newTestRouter(handler).ServeHTTP(rec, req)

	require.Equal(t, "caller-supplied", rec.Header().Get("X-Request-Id"))
	require.Equal(t, "caller-supplied", seen)
}

func TestRouter_GeneratesRequestIDWhenAbsent(t *testing.T) {
	t.Parallel()

	var seen string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = interceptors.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	newTestRouter(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	echoed := rec.Header().Get("X-Request-Id")
	require.NotEmpty(t, echoed)
	require.Equal(t, echoed, seen, "the generated id is the one the handler sees")
}

func TestRouter_RecoversPanic(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: secret")
	})

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		newTestRouter(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "INTERNAL", body.Code)
	require.Equal(t, "internal error", body.Message)
	require.NotContains(t, rec.Body.String(), "secret")
}

func TestRouter_WritesEnvelopeWithRequestID(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, status.Error(codes.NotFound, "profile not found"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	newTestRouter(handler).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "NOT_FOUND", body.Code)
	require.Equal(t, "abc-123", body.RequestID)
}
