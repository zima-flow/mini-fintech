package gatewayhttp

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxBodyBytes      = 1 << 20
	idempotencyHeader = "Idempotency-Key"
)

func decodeProto(w http.ResponseWriter, r *http.Request, msg proto.Message) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid request body")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return status.Error(codes.InvalidArgument, "empty request body")
	}
	if err := protojson.Unmarshal(data, msg); err != nil {
		return status.Error(codes.InvalidArgument, "invalid request body")
	}
	return nil
}

func writeProto(w http.ResponseWriter, r *http.Request, code int, msg proto.Message) {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "internal error"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

func idempotencyKey(r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get(idempotencyHeader))
	if key == "" {
		return "", false
	}
	return key, true
}

func apply(mw func(http.Handler) http.Handler, h http.Handler) http.Handler {
	if mw == nil {
		return h
	}
	return mw(h)
}
