package outbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type Message struct {
	ID         string
	Topic      string
	EventType  string
	Headers    map[string]string
	Payload    []byte
	OccurredAt time.Time
}

const insertSQL = `INSERT INTO outbox (id, topic, event_type, headers, payload, occurred_at) VALUES ($1, $2, $3, $4, $5, $6)`

func Enqueue(ctx context.Context, db DBTX, msg Message) error {
	if msg.ID == "" || msg.Topic == "" || msg.EventType == "" {
		return fmt.Errorf("outbox: enqueue: id, topic and event_type are required: %w", errs.ErrInvalidArgument)
	}
	if msg.OccurredAt.IsZero() {
		return fmt.Errorf("outbox: enqueue: occurred_at is required: %w", errs.ErrInvalidArgument)
	}

	headers := msg.Headers
	if headers == nil {
		headers = map[string]string{}
	}

	if _, err := db.Exec(ctx, insertSQL, msg.ID, msg.Topic, msg.EventType, headers, msg.Payload, msg.OccurredAt.UTC()); err != nil {
		return fmt.Errorf("outbox: enqueue %s: %w", msg.ID, err)
	}

	return nil
}

type Publisher interface {
	Publish(ctx context.Context, topic string, headers map[string]string, payload []byte) error
}

const (
	selectPendingSQL = `SELECT id, topic, event_type, headers, payload, occurred_at FROM outbox WHERE published_at IS NULL ORDER BY occurred_at LIMIT $1`
	markPublishedSQL = `UPDATE outbox SET published_at = $2 WHERE id = $1`

	defaultRelayBatch    = 100
	maxRelayBatch        = 1000
	defaultRelayInterval = 5 * time.Second
	maxRelayBackoff      = time.Minute

	metricOutboxPublished = "outbox.published"
	metricOutboxLag       = "outbox.lag"
)

type RelayConfig struct {
	DB         DBTX
	Publisher  Publisher
	Clock      clock.Clock
	Logger     *slog.Logger
	Interval   time.Duration
	Batch      int
	Tracer     trace.Tracer
	Propagator propagation.TextMapPropagator
	Meter      metric.Meter
}

type Relay struct {
	db         DBTX
	pub        Publisher
	clock      clock.Clock
	logger     *slog.Logger
	interval   time.Duration
	batch      int
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	published  metric.Int64Counter
	lag        metric.Int64Histogram
}

func NewRelay(cfg RelayConfig) *Relay {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultRelayInterval
	}

	batch := cfg.Batch
	if batch <= 0 {
		batch = defaultRelayBatch
	}
	if batch > maxRelayBatch {
		batch = maxRelayBatch
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	clk := cfg.Clock
	if clk == nil {
		clk = clock.Real{}
	}

	var published metric.Int64Counter
	var lag metric.Int64Histogram
	if cfg.Meter != nil {
		published, _ = cfg.Meter.Int64Counter(metricOutboxPublished,
			metric.WithDescription("Outbox messages published to the broker"))
		lag, _ = cfg.Meter.Int64Histogram(metricOutboxLag,
			metric.WithDescription("Age of the oldest unpublished outbox row per pass"),
			metric.WithUnit("ms"))
	}

	return &Relay{
		db:         cfg.DB,
		pub:        cfg.Publisher,
		clock:      clk,
		logger:     logger,
		interval:   interval,
		batch:      batch,
		tracer:     cfg.Tracer,
		propagator: cfg.Propagator,
		published:  published,
		lag:        lag,
	}
}

func (r *Relay) Run(ctx context.Context) {
	failures := 0

	for {
		n, err := r.PublishPending(ctx)

		delay := r.interval
		if err != nil {
			failures++
			delay = relayBackoff(r.interval, failures)
			r.logger.ErrorContext(ctx, "outbox relay pass failed",
				slog.Int("failures", failures),
				slog.Duration("retry_in", delay),
				slog.String("error", err.Error()),
			)
		} else {
			if failures > 0 {
				r.logger.InfoContext(ctx, "outbox relay recovered", slog.Int("failures", failures))
			}
			failures = 0
			if n > 0 {
				r.logger.DebugContext(ctx, "outbox relay published", slog.Int("count", n))
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-r.clock.After(delay):
		}
	}
}

func relayBackoff(interval time.Duration, failures int) time.Duration {
	if interval >= maxRelayBackoff {
		return interval
	}
	delay := interval
	for i := 1; i < failures; i++ {
		if delay >= maxRelayBackoff {
			return maxRelayBackoff
		}
		delay *= 2
	}
	if delay > maxRelayBackoff {
		return maxRelayBackoff
	}
	return delay
}

func (r *Relay) PublishPending(ctx context.Context) (int, error) {
	rows, err := r.db.Query(ctx, selectPendingSQL, r.batch)
	if err != nil {
		return 0, fmt.Errorf("outbox: query pending: %w", err)
	}
	defer rows.Close()

	type pending struct {
		id         string
		topic      string
		eventType  string
		headers    map[string]string
		payload    []byte
		occurredAt time.Time
	}

	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.topic, &p.eventType, &p.headers, &p.payload, &p.occurredAt); err != nil {
			return 0, fmt.Errorf("outbox: scan pending: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("outbox: read pending: %w", err)
	}

	if r.lag != nil && len(batch) > 0 {
		r.lag.Record(ctx, r.clock.Now().UTC().Sub(batch[0].occurredAt).Milliseconds())
	}

	published := 0
	for _, p := range batch {
		if err := r.publish(ctx, p.topic, p.headers, p.payload); err != nil {
			return published, fmt.Errorf("outbox: publish %s: %w", p.id, err)
		}
		if _, err := r.db.Exec(ctx, markPublishedSQL, p.id, r.clock.Now().UTC()); err != nil {
			return published, fmt.Errorf("outbox: mark published %s: %w", p.id, err)
		}
		if r.published != nil {
			r.published.Add(ctx, 1, metric.WithAttributes(
				attribute.String("topic", p.topic),
				attribute.String("event_type", p.eventType),
			))
		}
		published++
	}

	return published, nil
}

func (r *Relay) publish(ctx context.Context, topic string, headers map[string]string, payload []byte) error {
	if r.tracer == nil {
		return r.pub.Publish(ctx, topic, headers, payload)
	}

	msgCtx := ctx
	if r.propagator != nil {
		msgCtx = r.propagator.Extract(ctx, propagation.MapCarrier(headers))
	}
	msgCtx, span := r.tracer.Start(msgCtx, "publish "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.operation", "publish"),
		),
	)

	outHeaders := headers
	if r.propagator != nil {
		carrier := propagation.MapCarrier{}
		r.propagator.Inject(msgCtx, carrier)
		outHeaders = make(map[string]string, len(headers)+len(carrier))
		for k, v := range headers {
			outHeaders[k] = v
		}
		for k, v := range carrier {
			outHeaders[k] = v
		}
	}

	err := r.pub.Publish(msgCtx, topic, outHeaders, payload)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}
