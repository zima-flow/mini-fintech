package gatewayhttp

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const (
	RoleClient  = "CLIENT"
	RoleOfficer = "OFFICER"
	RoleAdmin   = "ADMIN"
)

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := interceptors.PrincipalFromContext(r.Context())
			if !ok {
				writeError(w, r, status.Error(codes.Unauthenticated, "missing access token"))
				return
			}
			for _, role := range principal.Roles {
				if _, ok := allowed[role]; ok {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeError(w, r, status.Error(codes.PermissionDenied, "insufficient role"))
		})
	}
}
