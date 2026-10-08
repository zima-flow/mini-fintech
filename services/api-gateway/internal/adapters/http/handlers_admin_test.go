package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
)

type fakeOfficerCreator struct {
	resp *authv1.CreateOfficerResponse
	err  error
	last *authv1.CreateOfficerRequest
}

func (f *fakeOfficerCreator) CreateOfficer(_ context.Context, in *authv1.CreateOfficerRequest, _ ...grpc.CallOption) (*authv1.CreateOfficerResponse, error) {
	f.last = in
	if f.resp == nil {
		return &authv1.CreateOfficerResponse{}, f.err
	}
	return f.resp, f.err
}

func TestAdminHandlers_CreateOfficer_Success(t *testing.T) {
	t.Parallel()

	creator := &fakeOfficerCreator{resp: &authv1.CreateOfficerResponse{
		UserId: "officer-1", Role: authv1.Role_ROLE_OFFICER,
	}}
	req := authRequest("/v1/admin/officers", `{"email":"o@b.com","password":"correct-horse"}`)
	req.Header.Set("Idempotency-Key", "idem-admin")
	rec := httptest.NewRecorder()
	NewAdminHandlers(creator).CreateOfficer(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "o@b.com", creator.last.GetEmail())
	require.Equal(t, "idem-admin", creator.last.GetIdempotencyKey())

	body := decodeBody(t, rec)
	require.Equal(t, "officer-1", body["user_id"])
	require.Equal(t, "ROLE_OFFICER", body["role"])
}

func TestAdminHandlers_CreateOfficer_MissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	creator := &fakeOfficerCreator{}
	rec := httptest.NewRecorder()
	NewAdminHandlers(creator).CreateOfficer(rec, authRequest("/v1/admin/officers", `{"email":"o@b.com","password":"pw"}`))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, creator.last)
}

func TestAdminHandlers_CreateOfficer_PermissionDenied(t *testing.T) {
	t.Parallel()

	creator := &fakeOfficerCreator{err: status.Error(codes.PermissionDenied, "admin role required")}
	req := authRequest("/v1/admin/officers", `{"email":"o@b.com","password":"pw"}`)
	req.Header.Set("Idempotency-Key", "idem-admin")
	rec := httptest.NewRecorder()
	NewAdminHandlers(creator).CreateOfficer(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRegisterAdminRoutes_RequiresAdmin(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	RegisterAdminRoutes(mux, NewAdminHandlers(&fakeOfficerCreator{resp: &authv1.CreateOfficerResponse{UserId: "officer-1"}}), authAs(RoleAdmin))

	req := authRequest("/v1/admin/officers", `{"email":"o@b.com","password":"pw"}`)
	req.Header.Set("Idempotency-Key", "idem-admin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	officerMux := http.NewServeMux()
	RegisterAdminRoutes(officerMux, NewAdminHandlers(&fakeOfficerCreator{}), authAs(RoleOfficer))
	req = authRequest("/v1/admin/officers", `{"email":"o@b.com","password":"pw"}`)
	req.Header.Set("Idempotency-Key", "idem-admin")
	officerMux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
