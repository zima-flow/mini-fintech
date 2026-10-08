package gatewayhttp

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
)

type AuthService interface {
	Register(ctx context.Context, in *authv1.RegisterRequest, opts ...grpc.CallOption) (*authv1.RegisterResponse, error)
	Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.LoginResponse, error)
	Refresh(ctx context.Context, in *authv1.RefreshRequest, opts ...grpc.CallOption) (*authv1.RefreshResponse, error)
	Logout(ctx context.Context, in *authv1.LogoutRequest, opts ...grpc.CallOption) (*authv1.LogoutResponse, error)
}

type AuthHandlers struct {
	auth AuthService
}

func NewAuthHandlers(auth AuthService) *AuthHandlers { return &AuthHandlers{auth: auth} }

func RegisterAuthRoutes(mux *http.ServeMux, h *AuthHandlers) {
	mux.HandleFunc("POST /v1/auth/register", h.Register)
	mux.HandleFunc("POST /v1/auth/login", h.Login)
	mux.HandleFunc("POST /v1/auth/refresh", h.Refresh)
	mux.HandleFunc("POST /v1/auth/logout", h.Logout)
}

func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req authv1.RegisterRequest
	if err := decodeProto(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	key, ok := idempotencyKey(r)
	if !ok {
		writeError(w, r, status.Error(codes.InvalidArgument, "missing Idempotency-Key header"))
		return
	}
	req.IdempotencyKey = key

	resp, err := h.auth.Register(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req authv1.LoginRequest
	if err := decodeProto(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	resp, err := h.auth.Login(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *AuthHandlers) Refresh(w http.ResponseWriter, r *http.Request) {
	var req authv1.RefreshRequest
	if err := decodeProto(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	resp, err := h.auth.Refresh(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	var req authv1.LogoutRequest
	if err := decodeProto(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	resp, err := h.auth.Logout(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}
