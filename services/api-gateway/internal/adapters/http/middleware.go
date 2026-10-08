package gatewayhttp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	platformid "github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/interceptors"
)

const requestIDHeader = "x-request-id"

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func recovery(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw, ok := w.(*statusWriter)
		if !ok {
			sw = &statusWriter{ResponseWriter: w}
		}

		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			cfg.Logger.ErrorContext(r.Context(), "http panic recovered",
				slog.Any("panic", rec),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			if sw.wroteHeader {
				return
			}
			writeJSON(sw, http.StatusInternalServerError, internalErrorResponse(r))
		}()

		next.ServeHTTP(sw, r)
	})
}

func requestID(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(requestIDHeader)
		if rid == "" {
			gen := cfg.RequestID
			if gen == nil {
				gen = platformid.UUIDv7{}
			}
			rid = gen.New()
		}
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r.WithContext(interceptors.WithRequestID(r.Context(), rid)))
	})
}

func tracing(cfg Config, next http.Handler) http.Handler {
	if cfg.Tracer == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if cfg.Propagator != nil {
			ctx = cfg.Propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))
		}
		ctx, span := cfg.Tracer.Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		span.SetAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
		)

		next.ServeHTTP(w, r.WithContext(ctx))

		if sw, ok := w.(*statusWriter); ok {
			span.SetAttributes(attribute.Int("http.response.status_code", sw.status))
			if sw.status >= http.StatusInternalServerError {
				span.SetStatus(otelcodes.Error, http.StatusText(sw.status))
			}
		}
	})
}

func logging(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)

		status := http.StatusOK
		if sw, ok := w.(*statusWriter); ok && sw.wroteHeader {
			status = sw.status
		}
		cfg.Logger.LogAttrs(r.Context(), levelForStatus(status), "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("request_id", interceptors.RequestIDFromContext(r.Context())),
			slog.String("trace_id", traceIDFromContext(r.Context())),
		)
	})
}

func levelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func traceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

func internalErrorResponse(r *http.Request) ErrorResponse {
	return ErrorResponse{
		Code:      "INTERNAL",
		Message:   "internal error",
		Details:   []string{},
		RequestID: interceptors.RequestIDFromContext(r.Context()),
	}
}
