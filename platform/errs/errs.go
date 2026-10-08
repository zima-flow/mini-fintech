package errs

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrFailedPrecondition = errors.New("failed precondition")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrConflict           = errors.New("conflict")
	ErrUnavailable        = errors.New("unavailable")
)

func ToStatus(err error) *status.Status {
	switch {
	case err == nil:
		return status.New(codes.OK, "")
	case errors.Is(err, ErrNotFound):
		return status.New(codes.NotFound, ErrNotFound.Error())
	case errors.Is(err, ErrAlreadyExists):
		return status.New(codes.AlreadyExists, ErrAlreadyExists.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.New(codes.InvalidArgument, ErrInvalidArgument.Error())
	case errors.Is(err, ErrFailedPrecondition):
		return status.New(codes.FailedPrecondition, ErrFailedPrecondition.Error())
	case errors.Is(err, ErrPermissionDenied):
		return status.New(codes.PermissionDenied, ErrPermissionDenied.Error())
	case errors.Is(err, ErrUnauthenticated):
		return status.New(codes.Unauthenticated, ErrUnauthenticated.Error())
	case errors.Is(err, ErrConflict):
		return status.New(codes.FailedPrecondition, ErrConflict.Error())
	case errors.Is(err, ErrUnavailable):
		return status.New(codes.Unavailable, ErrUnavailable.Error())
	default:
		return status.New(codes.Internal, "internal error")
	}
}
