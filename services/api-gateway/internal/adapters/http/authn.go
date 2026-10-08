package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const authorizationHeader = "Authorization"

const bearerPrefix = "Bearer "

type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (authn.Claims, error)
}

func RequireAuth(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if verifier == nil {
				writeError(w, r, status.Error(codes.Unavailable, "authentication unavailable"))
				return
			}

			header := r.Header.Get(authorizationHeader)
			raw, ok := bearerToken(header)
			if !ok {
				writeError(w, r, status.Error(codes.Unauthenticated, "missing access token"))
				return
			}

			claims, err := verifier.Verify(r.Context(), raw)
			if err != nil {
				if errors.Is(err, errs.ErrUnavailable) {
					writeError(w, r, status.Error(codes.Unavailable, "authentication unavailable"))
					return
				}
				writeError(w, r, status.Error(codes.Unauthenticated, "invalid access token"))
				return
			}

			ctx := interceptors.WithPrincipal(r.Context(), interceptors.Principal{
				Subject:    claims.Subject,
				Roles:      claims.Roles,
				CustomerID: claims.CustomerID,
			})
			ctx = interceptors.WithAuthorization(ctx, header)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(header string) (string, bool) {
	if !strings.HasPrefix(header, bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
	if token == "" {
		return "", false
	}
	return token, true
}
