package interceptors

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

type authenticator struct {
	verifier *authn.Verifier
	public   map[string]struct{}
	cfgErr   error
}

func newAuthenticator(cfg AuthConfig) *authenticator {
	public := make(map[string]struct{}, len(cfg.PublicMethods)+1)
	for _, method := range cfg.PublicMethods {
		public[method] = struct{}{}
	}
	verifier, err := authn.NewVerifier(authn.Config{
		JWKSURL:  cfg.JWKSURL,
		CacheTTL: cfg.CacheTTL,
		Issuer:   cfg.Issuer,
		Audience: cfg.Audience,
		Dev:      cfg.Dev,
	})
	return &authenticator{verifier: verifier, public: public, cfgErr: err}
}

func (a *authenticator) isPublic(method string) bool {
	if _, ok := a.public[method]; ok {
		return true
	}
	return strings.HasPrefix(method, healthPrefix)
}

func (a *authenticator) authenticate(ctx context.Context) (Principal, error) {
	if a.cfgErr != nil {
		return Principal{}, status.Error(codes.FailedPrecondition, "authentication is misconfigured")
	}

	raw, err := bearerFromIncoming(ctx)
	if err != nil {
		return Principal{}, err
	}

	claims, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		if errors.Is(err, errs.ErrUnavailable) {
			return Principal{}, status.Error(codes.Unavailable, "authentication unavailable")
		}
		return Principal{}, status.Error(codes.Unauthenticated, "invalid access token")
	}

	return Principal{
		Subject:    claims.Subject,
		Roles:      claims.Roles,
		CustomerID: claims.CustomerID,
	}, nil
}

func bearerFromIncoming(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing access token")
	}
	values := md.Get(authorization)
	if len(values) == 0 || values[0] == "" {
		return "", status.Error(codes.Unauthenticated, "missing access token")
	}
	header := values[0]
	if !strings.HasPrefix(header, bearerPrefix) {
		return "", status.Error(codes.Unauthenticated, "invalid authorization scheme")
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
	if token == "" {
		return "", status.Error(codes.Unauthenticated, "missing access token")
	}
	return token, nil
}

func unaryAuth(a *authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		mark(ctx, nameAuth)
		if a.isPublic(info.FullMethod) {
			return handler(ctx, req)
		}
		principal, err := a.authenticate(ctx)
		if err != nil {
			return nil, err
		}
		return handler(WithPrincipal(ctx, principal), req)
	}
}

func streamAuth(a *authenticator) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		mark(ctx, nameAuth)
		if a.isPublic(info.FullMethod) {
			return handler(srv, ss)
		}
		principal, err := a.authenticate(ctx)
		if err != nil {
			return err
		}
		return handler(srv, &contextStream{ServerStream: ss, ctx: WithPrincipal(ctx, principal)})
	}
}
