package gatewayhttp

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/auth/v1"
)

type OfficerCreator interface {
	CreateOfficer(ctx context.Context, in *authv1.CreateOfficerRequest, opts ...grpc.CallOption) (*authv1.CreateOfficerResponse, error)
}

type AdminHandlers struct {
	auth OfficerCreator
}

func NewAdminHandlers(auth OfficerCreator) *AdminHandlers { return &AdminHandlers{auth: auth} }

func RegisterAdminRoutes(mux *http.ServeMux, h *AdminHandlers, authenticate func(http.Handler) http.Handler) {
	admin := func(next http.Handler) http.Handler {
		return apply(authenticate, RequireRole(RoleAdmin)(next))
	}
	mux.Handle("POST /v1/admin/officers", admin(http.HandlerFunc(h.CreateOfficer)))
}

func (h *AdminHandlers) CreateOfficer(w http.ResponseWriter, r *http.Request) {
	var req authv1.CreateOfficerRequest
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

	resp, err := h.auth.CreateOfficer(r.Context(), &req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, r, http.StatusOK, resp)
}
