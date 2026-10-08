package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

func TestToStatus_MapsDomainErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		code codes.Code
	}{
		"not_found":           {fmt.Errorf("x: %w", errs.ErrNotFound), codes.NotFound},
		"already_exists":      {errs.ErrAlreadyExists, codes.AlreadyExists},
		"invalid_argument":    {errs.ErrInvalidArgument, codes.InvalidArgument},
		"failed_precondition": {errs.ErrFailedPrecondition, codes.FailedPrecondition},
		"permission_denied":   {errs.ErrPermissionDenied, codes.PermissionDenied},
		"unauthenticated":     {errs.ErrUnauthenticated, codes.Unauthenticated},
		"conflict":            {errs.ErrConflict, codes.FailedPrecondition},
		"unavailable":         {errs.ErrUnavailable, codes.Unavailable},
		"unknown":             {errors.New("boom"), codes.Internal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.code, errs.ToStatus(tc.err).Code())
		})
	}
}

func TestToStatus_UnknownDoesNotLeakInternalMessage(t *testing.T) {
	t.Parallel()
	internal := errors.New(`pq: relation "users" does not exist`)
	st := errs.ToStatus(fmt.Errorf("register user: %w", internal))
	require.Equal(t, codes.Internal, st.Code())
	require.NotContains(t, st.Message(), "relation")
	require.NotContains(t, st.Message(), "register user")
	require.NotEmpty(t, st.Message())
}
