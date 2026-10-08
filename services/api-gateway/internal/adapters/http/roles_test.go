package gatewayhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

func roleProbe(roles []string, principal *interceptors.Principal, reached *bool) http.Handler {
	handler := RequireRole(roles...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reached != nil {
			*reached = true
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if principal == nil {
		return newTestRouter(handler)
	}
	return newTestRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(interceptors.WithPrincipal(r.Context(), *principal)))
	}))
}

func TestRequireRole_AllowsMatchingRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{RoleClient, RoleOfficer, RoleAdmin} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			reached := false
			req := httptest.NewRequest(http.MethodGet, "/v1/customer/profile", nil)
			rec := httptest.NewRecorder()
			roleProbe([]string{role}, &interceptors.Principal{Roles: []string{role}}, &reached).ServeHTTP(rec, req)

			require.Equal(t, http.StatusNoContent, rec.Code)
			require.True(t, reached)
		})
	}
}

func TestRequireRole_DeniesWrongRole(t *testing.T) {
	t.Parallel()

	reached := false
	req := httptest.NewRequest(http.MethodGet, "/v1/officers/customers/abc", nil)
	rec := httptest.NewRecorder()
	roleProbe([]string{RoleOfficer, RoleAdmin}, &interceptors.Principal{Roles: []string{RoleClient}}, &reached).ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "PERMISSION_DENIED", decodeEnvelope(t, rec).Code)
	require.False(t, reached)
}

func TestRequireRole_MissingPrincipal(t *testing.T) {
	t.Parallel()

	reached := false
	rec := httptest.NewRecorder()
	roleProbe([]string{RoleAdmin}, nil, &reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/officers", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHENTICATED", decodeEnvelope(t, rec).Code)
	require.False(t, reached)
}

func TestRequireRole_NoRolesDeniesEveryone(t *testing.T) {
	t.Parallel()

	reached := false
	rec := httptest.NewRecorder()
	roleProbe(nil, &interceptors.Principal{Roles: []string{RoleAdmin}}, &reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/officers", nil))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, reached)
}

func TestRequireAuthThenRequireRole_Composed(t *testing.T) {
	t.Parallel()

	verifier := &fakeVerifier{claims: authn.Claims{Subject: "user-1", Roles: []string{RoleClient}}}
	reached := false
	guarded := RequireAuth(verifier)(RequireRole(RoleOfficer, RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/v1/officers/customers/abc", nil)
	req.Header.Set("Authorization", "Bearer client-token")
	rec := httptest.NewRecorder()
	newTestRouter(guarded).ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "PERMISSION_DENIED", decodeEnvelope(t, rec).Code)
	require.False(t, reached)
}
