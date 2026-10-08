package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const maxStatementLen = 1024

type queryTracer struct {
	tracer trace.Tracer
}

func newQueryTracer(tracer trace.Tracer) pgx.QueryTracer {
	if tracer == nil {
		return nil
	}
	return &queryTracer{tracer: tracer}
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	//nolint:spancheck // the span is ended by TraceQueryEnd via the returned context.
	ctx, _ = t.tracer.Start(ctx, statement(data.SQL),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", statement(data.SQL)),
		),
	)
	return ctx
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
	span.End()
}

func statement(sql string) string {
	if len(sql) <= maxStatementLen {
		return sql
	}
	return sql[:maxStatementLen]
}
