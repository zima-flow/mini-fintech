package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

type fakeVerifier struct {
	claims authn.Claims
	err    error
	raw    string
	called bool
}

func (f *fakeVerifier) Verify(_ context.Context, raw string) (authn.Claims, error) {
	f.called = true
	f.raw = raw
	return f.claims, f.err
}

func authProbe(verifier TokenVerifier, reached *bool) http.Handler {
	return newTestRouter(RequireAuth(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reached != nil {
			*reached = true
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestRequireAuth_MissingToken(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{}
	reached := false
	rec := httptest.NewRecorder()
	authProbe(verifier, &reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHENTICATED", decodeEnvelope(t, rec).Code)
	require.False(t, reached)
	require.False(t, verifier.called, "a missing token must not reach the verifier")
}

func TestRequireAuth_MalformedScheme(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"Basic abc", "Bearer", "Bearer   "} {
		header := header
		t.Run(header, func(t *testing.T) {
			t.Parallel()

			verifier := &fakeVerifier{}
			req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			authProbe(verifier, nil).ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.False(t, verifier.called)
		})
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{err: errs.ErrUnauthenticated}
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Authorization", "Bearer expired")
	rec := httptest.NewRecorder()
	authProbe(verifier, nil).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHENTICATED", decodeEnvelope(t, rec).Code)
}

func TestRequireAuth_VerifierUnavailable(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{err: errs.ErrUnavailable}
	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	authProbe(verifier, nil).ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "UNAVAILABLE", decodeEnvelope(t, rec).Code)
}

func TestRequireAuth_ValidTokenInstallsPrincipal(t *testing.T) {
	t.Parallel()

	want := authn.Claims{Subject: "user-1", Roles: []string{"CLIENT"}, CustomerID: "cust-1"}
	verifier := &fakeVerifier{claims: want}

	var got authn.Claims
	var seenAuth string
	handler := RequireAuth(verifier)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p, ok := interceptors.PrincipalFromContext(r.Context())
		require.True(t, ok)
		got = authn.Claims{Subject: p.Subject, Roles: p.Roles, CustomerID: p.CustomerID}
		seenAuth = interceptors.AuthorizationFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Authorization", "Bearer edge-token")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, verifier.called)
	require.Equal(t, "edge-token", verifier.raw, "the verifier receives the token without the scheme")
	require.Equal(t, want, got)
	require.Equal(t, "Bearer edge-token", seenAuth, "the raw header is forwarded unchanged")
}

func TestRequireAuth_NilVerifierFailsClosed(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
	req.Header.Set("Authorization", "Bearer edge-token")
	rec := httptest.NewRecorder()
	authProbe(nil, nil).ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
