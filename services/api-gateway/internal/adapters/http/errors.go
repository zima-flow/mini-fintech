package gatewayhttp

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

type ErrorResponse struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Details   []string `json:"details"`
	RequestID string   `json:"request_id"`
}

func HTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.FailedPrecondition:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func errorStatus(err error) *status.Status {
	if st, ok := status.FromError(err); ok && st != nil {
		if code := st.Code(); code == codes.Internal || code == codes.Unknown {
			return status.New(codes.Internal, "internal error")
		}
		return st
	}

	st := errs.ToStatus(err)
	if st.Code() == codes.Internal {
		return status.New(codes.Internal, "internal error")
	}
	return st
}

func codeName(code codes.Code) string {
	switch code {
	case codes.OK:
		return "OK"
	case codes.Canceled:
		return "CANCELLED"
	case codes.Unknown:
		return "UNKNOWN"
	case codes.InvalidArgument:
		return "INVALID_ARGUMENT"
	case codes.DeadlineExceeded:
		return "DEADLINE_EXCEEDED"
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.ResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case codes.FailedPrecondition:
		return "FAILED_PRECONDITION"
	case codes.Aborted:
		return "ABORTED"
	case codes.OutOfRange:
		return "OUT_OF_RANGE"
	case codes.Unimplemented:
		return "UNIMPLEMENTED"
	case codes.Internal:
		return "INTERNAL"
	case codes.Unavailable:
		return "UNAVAILABLE"
	case codes.DataLoss:
		return "DATA_LOSS"
	case codes.Unauthenticated:
		return "UNAUTHENTICATED"
	default:
		return "UNKNOWN"
	}
}

func errorResponse(r *http.Request, err error) (int, ErrorResponse) {
	st := errorStatus(err)
	return HTTPStatus(st.Code()), ErrorResponse{
		Code:      codeName(st.Code()),
		Message:   st.Message(),
		Details:   []string{},
		RequestID: interceptors.RequestIDFromContext(r.Context()),
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	code, body := errorResponse(r, err)
	writeJSON(w, code, body)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"code":"INTERNAL","message":"internal error","details":[],"request_id":""}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}
