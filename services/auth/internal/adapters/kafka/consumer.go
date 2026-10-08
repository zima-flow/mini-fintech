package kafkaadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	platformkafka "github.com/zima-flow/go-mentor/mini-fintech/platform/kafka"
	platformotel "github.com/zima-flow/go-mentor/mini-fintech/platform/otel"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
)

type Linker interface {
	LinkCustomer(ctx context.Context, cmd app.LinkCustomerCommand) error
}

const defaultRetryInterval = 5 * time.Second

type ConsumerConfig struct {
	Client        *kgo.Client
	Linker        Linker
	Clock         clock.Clock
	Logger        *slog.Logger
	RetryInterval time.Duration
	Propagator    propagation.TextMapPropagator
	Tracer        trace.Tracer
}

type Consumer struct {
	client     *kgo.Client
	linker     Linker
	clock      clock.Clock
	logger     *slog.Logger
	retry      time.Duration
	propagator propagation.TextMapPropagator
	tracer     trace.Tracer
}

func NewConsumer(cfg ConsumerConfig) *Consumer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	retry := cfg.RetryInterval
	if retry <= 0 {
		retry = defaultRetryInterval
	}
	return &Consumer{client: cfg.Client, linker: cfg.Linker, clock: clk, logger: logger, retry: retry, propagator: cfg.Propagator, tracer: cfg.Tracer}
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil
		}
		if err := fetches.Err(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("kafka: poll: %w", err)
		}

		iter := fetches.RecordIter()
		for !iter.Done() {
			record := iter.Next()
			if err := c.handle(ctx, record, c.client.CommitRecords); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (c *Consumer) handle(ctx context.Context, record *kgo.Record, commit func(context.Context, ...*kgo.Record) error) error {
	ctx, span := c.startSpan(ctx, record)
	defer span.End()

	if err := c.dispatch(ctx, record); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if err := commit(ctx, record); err != nil {
		err = fmt.Errorf("kafka: commit offset %s/%d/%d: %w", record.Topic, record.Partition, record.Offset, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (c *Consumer) startSpan(ctx context.Context, record *kgo.Record) (context.Context, trace.Span) {
	ctx = platformotel.ContextWithHeaders(ctx, c.propagator, platformkafka.RecordHeaders(record))
	if c.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	//nolint:spancheck // the span is returned and ended by handle.
	return c.tracer.Start(ctx, "process "+record.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.source.name", record.Topic),
			attribute.String("messaging.operation", "receive"),
		),
	)
}

func (c *Consumer) dispatch(ctx context.Context, record *kgo.Record) error {
	envelope, err := events.Decode(record.Value)
	if err != nil {
		c.logger.ErrorContext(ctx, "skipping undecodable event", slog.String("error", err.Error()))
		return nil
	}

	switch envelope.GetEventType() {
	case events.TypeProfileCreated:
		payload := envelope.GetProfileCreated()
		if payload == nil {
			c.logger.ErrorContext(ctx, "profile_created has no payload", slog.String("event_id", envelope.GetEventId()))
			return nil
		}

		return c.deliver(ctx, app.LinkCustomerCommand{
			EventID:    envelope.GetEventId(),
			EventType:  envelope.GetEventType(),
			UserID:     payload.GetUserId(),
			CustomerID: payload.GetCustomerId(),
		})
	default:
		c.logger.DebugContext(ctx, "ignoring foreign event", slog.String("event_type", envelope.GetEventType()))
		return nil
	}
}

func (c *Consumer) deliver(ctx context.Context, cmd app.LinkCustomerCommand) error {
	for {
		err := c.linker.LinkCustomer(ctx, cmd)
		if err == nil {
			return nil
		}
		if errors.Is(err, errs.ErrNotFound) {
			c.logger.WarnContext(ctx, "nothing to link for profile_created",
				slog.String("event_id", cmd.EventID),
				slog.String("user_id", cmd.UserID),
			)
			return nil
		}

		c.logger.ErrorContext(ctx, "linking customer failed; retrying",
			slog.String("event_id", cmd.EventID),
			slog.String("error", err.Error()),
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.clock.After(c.retry):
		}
	}
}
