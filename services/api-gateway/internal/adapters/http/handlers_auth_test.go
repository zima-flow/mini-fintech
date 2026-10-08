package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
)

type fakeAuth struct {
	registerResp *authv1.RegisterResponse
	registerErr  error
	loginResp    *authv1.LoginResponse
	loginErr     error
	refreshResp  *authv1.RefreshResponse
	refreshErr   error
	logoutResp   *authv1.LogoutResponse
	logoutErr    error

	lastRegister *authv1.RegisterRequest
	lastLogin    *authv1.LoginRequest
	lastRefresh  *authv1.RefreshRequest
	lastLogout   *authv1.LogoutRequest
}

func (f *fakeAuth) Register(_ context.Context, in *authv1.RegisterRequest, _ ...grpc.CallOption) (*authv1.RegisterResponse, error) {
	f.lastRegister = in
	if f.registerResp == nil {
		return &authv1.RegisterResponse{}, f.registerErr
	}
	return f.registerResp, f.registerErr
}

func (f *fakeAuth) Login(_ context.Context, in *authv1.LoginRequest, _ ...grpc.CallOption) (*authv1.LoginResponse, error) {
	f.lastLogin = in
	if f.loginResp == nil {
		return &authv1.LoginResponse{}, f.loginErr
	}
	return f.loginResp, f.loginErr
}

func (f *fakeAuth) Refresh(_ context.Context, in *authv1.RefreshRequest, _ ...grpc.CallOption) (*authv1.RefreshResponse, error) {
	f.lastRefresh = in
	if f.refreshResp == nil {
		return &authv1.RefreshResponse{}, f.refreshErr
	}
	return f.refreshResp, f.refreshErr
}

func (f *fakeAuth) Logout(_ context.Context, in *authv1.LogoutRequest, _ ...grpc.CallOption) (*authv1.LogoutResponse, error) {
	f.lastLogout = in
	if f.logoutResp == nil {
		return &authv1.LogoutResponse{}, f.logoutErr
	}
	return f.logoutResp, f.logoutErr
}

func authRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestAuthHandlers_Register_Success(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{registerResp: &authv1.RegisterResponse{
		UserId: "user-1", CustomerId: "cust-1", Role: authv1.Role_ROLE_CLIENT,
	}}
	req := authRequest("/v1/auth/register", `{"email":"a@b.com","password":"correct-horse"}`)
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Register(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "a@b.com", auth.lastRegister.GetEmail())
	require.Equal(t, "correct-horse", auth.lastRegister.GetPassword())
	require.Equal(t, "idem-1", auth.lastRegister.GetIdempotencyKey())

	body := decodeBody(t, rec)
	require.Equal(t, "user-1", body["user_id"])
	require.Equal(t, "cust-1", body["customer_id"])
	require.Equal(t, "ROLE_CLIENT", body["role"])
}

func TestAuthHandlers_Register_MissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Register(rec, authRequest("/v1/auth/register", `{"email":"a@b.com","password":"pw"}`))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_ARGUMENT", decodeEnvelope(t, rec).Code)
	require.Nil(t, auth.lastRegister, "the auth service must not be called without a key")
}

func TestAuthHandlers_Register_InvalidBody(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{}
	req := authRequest("/v1/auth/register", `{not json`)
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Register(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_ARGUMENT", decodeEnvelope(t, rec).Code)
	require.Nil(t, auth.lastRegister)
}

func TestAuthHandlers_Register_UnknownFieldRejected(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{}
	req := authRequest("/v1/auth/register", `{"email":"a@b.com","password":"pw","admin":true}`)
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Register(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, auth.lastRegister)
}

func TestAuthHandlers_Register_UpstreamError(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{registerErr: status.Error(codes.AlreadyExists, "email already registered")}
	req := authRequest("/v1/auth/register", `{"email":"a@b.com","password":"pw"}`)
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Register(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	body := decodeEnvelope(t, rec)
	require.Equal(t, "ALREADY_EXISTS", body.Code)
	require.Equal(t, "email already registered", body.Message)
}

func TestAuthHandlers_Login_Success(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{loginResp: &authv1.LoginResponse{
		Tokens: &authv1.TokenPair{AccessToken: "access-1", RefreshToken: "refresh-1"},
		Role:   authv1.Role_ROLE_CLIENT, UserId: "user-1", CustomerId: "cust-1",
	}}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Login(rec, authRequest("/v1/auth/login", `{"email":"a@b.com","password":"pw"}`))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "a@b.com", auth.lastLogin.GetEmail())

	body := decodeBody(t, rec)
	tokens, ok := body["tokens"].(map[string]any)
	require.True(t, ok, "login response carries a nested tokens object")
	require.Equal(t, "access-1", tokens["access_token"])
	require.Equal(t, "refresh-1", tokens["refresh_token"])
	require.Equal(t, "user-1", body["user_id"])
}

func TestAuthHandlers_Login_UpstreamUnauthenticated(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{loginErr: status.Error(codes.Unauthenticated, "invalid credentials")}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Login(rec, authRequest("/v1/auth/login", `{"email":"a@b.com","password":"pw"}`))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHENTICATED", decodeEnvelope(t, rec).Code)
}

func TestAuthHandlers_Refresh_Success(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{refreshResp: &authv1.RefreshResponse{
		Tokens: &authv1.TokenPair{AccessToken: "access-2", RefreshToken: "refresh-2"},
	}}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Refresh(rec, authRequest("/v1/auth/refresh", `{"refresh_token":"refresh-1"}`))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "refresh-1", auth.lastRefresh.GetRefreshToken())
	require.Equal(t, "access-2", decodeBody(t, rec)["tokens"].(map[string]any)["access_token"])
}

func TestAuthHandlers_Logout_Success(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{logoutResp: &authv1.LogoutResponse{}}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Logout(rec, authRequest("/v1/auth/logout", `{"refresh_token":"refresh-1"}`))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "refresh-1", auth.lastLogout.GetRefreshToken())
}

func TestAuthHandlers_Logout_UpstreamError(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{logoutErr: status.Error(codes.Unauthenticated, "invalid refresh token")}
	rec := httptest.NewRecorder()
	NewAuthHandlers(auth).Logout(rec, authRequest("/v1/auth/logout", `{"refresh_token":"stale"}`))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHandlers_Routes(t *testing.T) {
	t.Parallel()

	auth := &fakeAuth{}
	mux := http.NewServeMux()
	RegisterAuthRoutes(mux, NewAuthHandlers(auth))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authRequest("/v1/auth/login", `{"email":"a@b.com","password":"pw"}`))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, auth.lastLogin)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code, "GET is not registered")
}
