package gatewayhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

func TestHTTPStatus_MappingTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code codes.Code
		want int
	}{
		{codes.OK, http.StatusOK},
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.Unauthenticated, http.StatusUnauthorized},
		{codes.PermissionDenied, http.StatusForbidden},
		{codes.NotFound, http.StatusNotFound},
		{codes.AlreadyExists, http.StatusConflict},
		{codes.FailedPrecondition, http.StatusConflict},
		{codes.ResourceExhausted, http.StatusTooManyRequests},
		{codes.Unimplemented, http.StatusNotImplemented},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.Internal, http.StatusInternalServerError},
		{codes.Unknown, http.StatusInternalServerError},
		{codes.DataLoss, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, HTTPStatus(tc.code), "code %s", tc.code)
	}
}

func TestWriteError_GRPCStatus(t *testing.T) {
	t.Parallel()

	rec := newRecordingRequest()
	writeError(rec.w, rec.r, status.Error(codes.FailedPrecondition, "profile is locked"))

	require.Equal(t, http.StatusConflict, rec.w.Code)
	body := rec.decode(t)
	require.Equal(t, "FAILED_PRECONDITION", body.Code)
	require.Equal(t, "profile is locked", body.Message)
	require.Empty(t, body.Details)
	require.Equal(t, "req-1", body.RequestID)
}

func TestWriteError_DomainSentinel(t *testing.T) {
	t.Parallel()

	rec := newRecordingRequest()
	writeError(rec.w, rec.r, fmt.Errorf("load profile: %w", errs.ErrNotFound))

	require.Equal(t, http.StatusNotFound, rec.w.Code)
	body := rec.decode(t)
	require.Equal(t, "NOT_FOUND", body.Code)
	require.Equal(t, "not found", body.Message)
}

func TestWriteError_UnknownIsGeneric(t *testing.T) {
	t.Parallel()

	rec := newRecordingRequest()
	writeError(rec.w, rec.r, errors.New("database password is hunter2"))

	require.Equal(t, http.StatusInternalServerError, rec.w.Code)
	body := rec.decode(t)
	require.Equal(t, "INTERNAL", body.Code)
	require.Equal(t, "internal error", body.Message)
	require.NotContains(t, rec.w.Body.String(), "hunter2")
	require.Contains(t, rec.w.Body.String(), `"details":[]`, "details is always an empty array")
}

func TestWriteError_InternalMessageSanitized(t *testing.T) {
	t.Parallel()

	rec := newRecordingRequest()
	writeError(rec.w, rec.r, status.Error(codes.Internal, "connection refused to 10.0.0.5"))

	require.Equal(t, http.StatusInternalServerError, rec.w.Code)
	body := rec.decode(t)
	require.Equal(t, "internal error", body.Message)
	require.NotContains(t, rec.w.Body.String(), "10.0.0.5")
}

type recordingRequest struct {
	w *httptest.ResponseRecorder
	r *http.Request
}

func newRecordingRequest() *recordingRequest {
	r := httptest.NewRequest(http.MethodGet, "/v1/example", nil)
	r = r.WithContext(interceptors.WithRequestID(r.Context(), "req-1"))
	return &recordingRequest{w: httptest.NewRecorder(), r: r}
}

func (rr *recordingRequest) decode(t *testing.T) ErrorResponse {
	t.Helper()

	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rr.w.Body.Bytes(), &body))
	return body
}
