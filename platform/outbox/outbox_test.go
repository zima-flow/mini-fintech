package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
)

func attrString(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	require.Failf(t, "attribute not found", "span %q has no attribute %q", span.Name(), key)
	return ""
}

func sumMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) (metricdata.Sum[int64], bool) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				sum, ok := m.Data.(metricdata.Sum[int64])
				return sum, ok
			}
		}
	}
	return metricdata.Sum[int64]{}, false
}

func histMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) (metricdata.Histogram[int64], bool) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				hist, ok := m.Data.(metricdata.Histogram[int64])
				return hist, ok
			}
		}
	}
	return metricdata.Histogram[int64]{}, false
}

// fakes

type execCall struct {
	sql  string
	args []any
}

type fakeDBTX struct {
	mu        sync.Mutex
	execCalls []execCall
	execErr   error

	rows     [][]any
	queryErr error
	queries  int
	querySQL string
}

func (f *fakeDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCalls = append(f.execCalls, execCall{sql: sql, args: args})
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (f *fakeDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	f.querySQL = sql
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &fakeRows{rows: f.rows}, nil
}

func (f *fakeDBTX) execs() []execCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execCall(nil), f.execCalls...)
}

func (f *fakeDBTX) queryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries
}

type fakeRows struct {
	rows [][]any
	idx  int
	cur  []any
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 0") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return r.cur, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return pgtype.NewMap() }

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		r.cur = nil
		return false
	}
	r.cur = r.rows[r.idx]
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if len(dest) != len(r.cur) {
		return fmt.Errorf("fakeRows: scan %d destinations into %d columns", len(dest), len(r.cur))
	}
	for i := range dest {
		if dest[i] == nil {
			continue
		}
		dv := reflect.ValueOf(dest[i])
		if dv.Kind() != reflect.Pointer || dv.IsNil() {
			return fmt.Errorf("fakeRows: destination %d is not a non-nil pointer", i)
		}
		dv.Elem().Set(reflect.ValueOf(r.cur[i]))
	}
	return nil
}

type publishedMessage struct {
	topic   string
	headers map[string]string
	payload []byte
}

type fakePublisher struct {
	mu       sync.Mutex
	messages []publishedMessage
}

func (f *fakePublisher) Publish(_ context.Context, topic string, headers map[string]string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, publishedMessage{topic: topic, headers: headers, payload: payload})
	return nil
}

func (f *fakePublisher) published() []publishedMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]publishedMessage(nil), f.messages...)
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	delays  []time.Duration
	pending chan time.Time
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delays = append(c.delays, d)
	c.pending = make(chan time.Time, 1)
	return c.pending
}

func (c *fakeClock) tick() {
	c.mu.Lock()
	ch := c.pending
	c.mu.Unlock()
	ch <- c.now
}

func (c *fakeClock) recordedDelays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitForDelays(t *testing.T, c *fakeClock, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.delays) >= n
	}, 2*time.Second, time.Millisecond, "relay did not schedule delay #%d", n)
}

func waitForQueries(t *testing.T, db *fakeDBTX, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return db.queryCount() >= n
	}, 2*time.Second, time.Millisecond, "relay did not run query #%d", n)
}

func requireRunStops(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Relay.Run did not return after context cancellation")
	}
}

// Enqueue

func TestEnqueue_InsertsOutboxRowWithHeaders(t *testing.T) {
	t.Parallel()

	headers := map[string]string{
		"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"x-request-id": "req-1",
	}
	msg := outbox.Message{
		ID:         "0192f0a0-0000-7000-8000-000000000001",
		Topic:      "kyc.events",
		EventType:  "kyc.application_approved",
		Headers:    headers,
		Payload:    []byte(`{"application_id":"a1"}`),
		OccurredAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600)),
	}

	db := &fakeDBTX{}
	require.NoError(t, outbox.Enqueue(context.Background(), db, msg))

	execs := db.execs()
	require.Len(t, execs, 1)
	call := execs[0]

	require.Contains(t, call.sql, "INSERT INTO outbox")
	for _, col := range []string{"id", "topic", "event_type", "headers", "payload", "occurred_at"} {
		require.Contains(t, call.sql, col, "insert must name the %s column", col)
	}
	require.Contains(t, call.sql, "$6", "insert must bind all six columns")

	require.Len(t, call.args, 6)
	require.Equal(t, msg.ID, call.args[0])
	require.Equal(t, msg.Topic, call.args[1])
	require.Equal(t, msg.EventType, call.args[2])
	require.Equal(t, headers, call.args[3], "headers must reach the database")
	require.Equal(t, msg.Payload, call.args[4])

	got, ok := call.args[5].(time.Time)
	require.True(t, ok, "occurred_at must be a time.Time")
	require.True(t, msg.OccurredAt.Equal(got), "occurred_at must preserve the instant")
	require.Equal(t, time.UTC, got.Location(), "occurred_at must be stored in UTC")
}

func TestEnqueue_DefaultsHeadersToEmptyMap(t *testing.T) {
	t.Parallel()

	db := &fakeDBTX{}
	require.NoError(t, outbox.Enqueue(context.Background(), db, outbox.Message{
		ID:         "0192f0a0-0000-7000-8000-000000000002",
		Topic:      "kyc.events",
		EventType:  "kyc.application_approved",
		OccurredAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	}))

	execs := db.execs()
	require.Len(t, execs, 1)
	require.Equal(t, map[string]string{}, execs[0].args[3], "a nil header map must persist as jsonb '{}'")
}

func TestEnqueue_RejectsIncompleteMessage(t *testing.T) {
	t.Parallel()

	valid := outbox.Message{
		ID:         "0192f0a0-0000-7000-8000-000000000001",
		Topic:      "kyc.events",
		EventType:  "kyc.application_approved",
		OccurredAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	}

	cases := map[string]func(m outbox.Message) outbox.Message{
		"missing_id":         func(m outbox.Message) outbox.Message { m.ID = ""; return m },
		"missing_topic":      func(m outbox.Message) outbox.Message { m.Topic = ""; return m },
		"missing_event_type": func(m outbox.Message) outbox.Message { m.EventType = ""; return m },
		"zero_occurred_at":   func(m outbox.Message) outbox.Message { m.OccurredAt = time.Time{}; return m },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := &fakeDBTX{}
			err := outbox.Enqueue(context.Background(), db, mutate(valid))

			require.ErrorIs(t, err, errs.ErrInvalidArgument)
			require.Empty(t, db.execs(), "invalid messages must not reach the database")
		})
	}
}

// PublishPending

func TestPublishPending_PublishesWithHeadersAndMarks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	headers := map[string]string{"traceparent": "00-trace-span-01"}
	db := &fakeDBTX{rows: [][]any{
		{"id-1", "auth.events", "auth.user_registered", headers, []byte("p1"), now},
		{"id-2", "customer.events", "customer.profile_created", map[string]string{}, []byte("p2"), now},
	}}
	pub := &fakePublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{DB: db, Publisher: pub, Clock: clock.Fixed{T: now}})

	n, err := relay.PublishPending(context.Background())

	require.NoError(t, err)
	require.Equal(t, 2, n)

	published := pub.published()
	require.Len(t, published, 2)
	require.Equal(t, "auth.events", published[0].topic)
	require.Equal(t, headers, published[0].headers, "headers must be delivered to the publisher")
	require.Equal(t, []byte("p1"), published[0].payload)
	require.Equal(t, "customer.events", published[1].topic)

	execs := db.execs()
	require.Len(t, execs, 2, "each delivered message must be marked published")
	require.Contains(t, execs[0].sql, "UPDATE outbox SET published_at")
	require.Equal(t, "id-1", execs[0].args[0])
}

func TestPublishPending_AtLeastOnceOnMarkFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	db := &fakeDBTX{
		rows:    [][]any{{"id-1", "auth.events", "auth.user_registered", map[string]string{"traceparent": "t"}, []byte("p1"), now}},
		execErr: errors.New("connection reset"),
	}
	pub := &fakePublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{DB: db, Publisher: pub, Clock: clock.Fixed{T: now}})

	n, err := relay.PublishPending(context.Background())

	require.Error(t, err)
	require.Equal(t, 0, n)
	require.Len(t, pub.published(), 1, "the message is delivered even though marking failed")

	db.mu.Lock()
	db.execErr = nil
	db.mu.Unlock()

	n, err = relay.PublishPending(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.published(), 2, "the unmarked message is redelivered: at-least-once")
}

func TestPublishPending_InjectsProducerSpanIntoHeaders(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := provider.Tracer("test")
	propagator := propagation.TraceContext{}

	const (
		stored          = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		storedTraceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
		storedParentID  = "00f067aa0ba902b7"
		requestIDHeader = "x-request-id"
	)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	db := &fakeDBTX{rows: [][]any{{
		"id-1", "auth.events", "auth.user_registered",
		map[string]string{"traceparent": stored, requestIDHeader: "req-7"},
		[]byte("p1"), now,
	}}}
	pub := &fakePublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: pub, Clock: clock.Fixed{T: now},
		Tracer: tracer, Propagator: propagator,
	})

	n, err := relay.PublishPending(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)

	published := pub.published()
	require.Len(t, published, 1)
	require.Equal(t, "req-7", published[0].headers[requestIDHeader], "non-trace headers must be preserved")

	carrier := propagation.MapCarrier(published[0].headers)
	sc := trace.SpanContextFromContext(propagator.Extract(context.Background(), carrier))
	require.True(t, sc.IsValid())
	require.Equal(t, storedTraceID, sc.TraceID().String(), "the trace id is preserved")
	require.NotEqual(t, storedParentID, sc.SpanID().String(), "the record carries the producer span, not the stored one")

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, trace.SpanKindProducer, ended[0].SpanKind())
	require.Equal(t, storedParentID, ended[0].Parent().SpanID().String(), "the producer span continues the stored context")
	require.Equal(t, sc.SpanID(), ended[0].SpanContext().SpanID())
	require.Equal(t, "auth.events", attrString(t, ended[0], "messaging.destination.name"))
}

func TestPublishPending_RecordsBusinessMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	occurred := now.Add(-3 * time.Second)
	db := &fakeDBTX{rows: [][]any{
		{"id-1", "auth.events", "auth.user_registered", map[string]string{}, []byte("p1"), occurred},
	}}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: &fakePublisher{}, Clock: clock.Fixed{T: now},
		Meter: provider.Meter("test"),
	})

	n, err := relay.PublishPending(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	published, ok := sumMetric(t, rm, "outbox.published")
	require.True(t, ok, "outbox.published must be an int64 counter")
	require.Len(t, published.DataPoints, 1)
	require.Equal(t, int64(1), published.DataPoints[0].Value)
	topic, ok := published.DataPoints[0].Attributes.Value("topic")
	require.True(t, ok)
	require.Equal(t, "auth.events", topic.AsString())
	eventType, ok := published.DataPoints[0].Attributes.Value("event_type")
	require.True(t, ok)
	require.Equal(t, "auth.user_registered", eventType.AsString())

	lag, ok := histMetric(t, rm, "outbox.lag")
	require.True(t, ok, "outbox.lag must be a histogram")
	require.Len(t, lag.DataPoints, 1)
	require.Equal(t, uint64(1), lag.DataPoints[0].Count)
	require.Equal(t, int64(3000), lag.DataPoints[0].Sum, "lag is the oldest pending age in ms")
}

// Relay.Run

func TestRelayRun_FirstPassIsImmediateThenWaitsForTicker(t *testing.T) {
	t.Parallel()

	fc := newFakeClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	db := &fakeDBTX{}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: &fakePublisher{}, Clock: fc, Logger: discardLogger(),
		Interval: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	waitForQueries(t, db, 1)
	waitForDelays(t, fc, 1)
	require.Equal(t, time.Second, fc.recordedDelays()[0])

	fc.tick()
	waitForQueries(t, db, 2)

	cancel()
	requireRunStops(t, done)

	require.Empty(t, db.execs())
}

func TestRelayRun_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	fc := newFakeClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	db := &fakeDBTX{}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: &fakePublisher{}, Clock: fc, Logger: discardLogger(),
		Interval: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	waitForDelays(t, fc, 1)

	cancel()
	requireRunStops(t, done)
}

func TestRelayRun_BacksOffExponentiallyOnRepeatedErrors(t *testing.T) {
	t.Parallel()

	fc := newFakeClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	db := &fakeDBTX{queryErr: errors.New("database unavailable")}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: &fakePublisher{}, Clock: fc, Logger: discardLogger(),
		Interval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	waitForDelays(t, fc, 1)
	require.Equal(t, 100*time.Millisecond, fc.recordedDelays()[0])

	fc.tick()
	waitForDelays(t, fc, 2)
	require.Equal(t, 200*time.Millisecond, fc.recordedDelays()[1])

	fc.tick()
	waitForDelays(t, fc, 3)
	require.Equal(t, 400*time.Millisecond, fc.recordedDelays()[2])

	cancel()
	requireRunStops(t, done)
}

func TestRelayRun_ResetsBackoffAfterSuccess(t *testing.T) {
	t.Parallel()

	fc := newFakeClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	db := &fakeDBTX{queryErr: errors.New("database unavailable")}
	relay := outbox.NewRelay(outbox.RelayConfig{
		DB: db, Publisher: &fakePublisher{}, Clock: fc, Logger: discardLogger(),
		Interval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	waitForDelays(t, fc, 1)
	fc.tick()
	waitForDelays(t, fc, 2)

	db.mu.Lock()
	db.queryErr = nil
	db.mu.Unlock()

	fc.tick()

	waitForDelays(t, fc, 3)
	require.Equal(t, 100*time.Millisecond, fc.recordedDelays()[2])

	cancel()
	requireRunStops(t, done)
}
