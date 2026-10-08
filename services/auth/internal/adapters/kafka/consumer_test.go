package kafkaadapter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
)

type immediateClock struct{}

func (immediateClock) Now() time.Time { return time.Now().UTC() }

func (immediateClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

type fakeLinker struct {
	cmds []app.LinkCustomerCommand
	ctxs []context.Context
	errs []error
}

func (l *fakeLinker) LinkCustomer(ctx context.Context, cmd app.LinkCustomerCommand) error {
	l.cmds = append(l.cmds, cmd)
	l.ctxs = append(l.ctxs, ctx)
	if len(l.errs) == 0 {
		return nil
	}
	err := l.errs[0]
	l.errs = l.errs[1:]
	return err
}

type commitRecorder struct {
	records []*kgo.Record
	err     error
}

func (c *commitRecorder) commit(_ context.Context, recs ...*kgo.Record) error {
	if c.err != nil {
		return c.err
	}
	c.records = append(c.records, recs...)
	return nil
}

func newTestConsumer(linker Linker, clk clock.Clock) *Consumer {
	return NewConsumer(ConsumerConfig{
		Linker: linker,
		Clock:  clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func profileCreatedRecord(t *testing.T, eventID string) *kgo.Record {
	t.Helper()

	env, err := events.Envelope(eventID, events.TypeProfileCreated, time.Now().UTC().Truncate(time.Second), &eventsv1.ProfileCreated{
		UserId:     "user-1",
		CustomerId: "cust-1",
	})
	require.NoError(t, err)
	data, err := events.Encode(env)
	require.NoError(t, err)
	return &kgo.Record{Topic: events.TopicCustomer, Partition: 0, Offset: 7, Value: data}
}

func TestConsumer_Handle_ProfileCreated_LinksThenCommits(t *testing.T) {
	t.Parallel()

	linker := &fakeLinker{}
	consumer := newTestConsumer(linker, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), profileCreatedRecord(t, "evt-1"), commit.commit))

	require.Len(t, linker.cmds, 1)
	require.Equal(t, app.LinkCustomerCommand{
		EventID:    "evt-1",
		EventType:  events.TypeProfileCreated,
		UserID:     "user-1",
		CustomerID: "cust-1",
	}, linker.cmds[0])
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_StartsConsumerSpan(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	linker := &fakeLinker{}
	consumer := NewConsumer(ConsumerConfig{
		Linker:     linker,
		Clock:      immediateClock{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Propagator: propagation.TraceContext{},
		Tracer:     provider.Tracer("test"),
	})
	commit := &commitRecorder{}

	record := profileCreatedRecord(t, "evt-trace")
	record.Headers = []kgo.RecordHeader{{Key: "traceparent", Value: []byte(traceparent)}}

	require.NoError(t, consumer.handle(context.Background(), record, commit.commit))

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, trace.SpanKindConsumer, ended[0].SpanKind())
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", ended[0].SpanContext().TraceID().String())
	require.Equal(t, "00f067aa0ba902b7", ended[0].Parent().SpanID().String(), "the consumer span continues the producer")
	require.Len(t, linker.ctxs, 1)
	require.Equal(t, ended[0].SpanContext().SpanID(), trace.SpanContextFromContext(linker.ctxs[0]).SpanID(),
		"the handler must run inside the consumer span")
}

func TestConsumer_Handle_ForeignEvent_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	env, err := events.Envelope("evt-2", events.TypeUserRegistered, time.Now().UTC(), &eventsv1.UserRegistered{
		UserId: "user-1", Email: "u@example.com", Role: "CLIENT",
	})
	require.NoError(t, err)
	data, err := events.Encode(env)
	require.NoError(t, err)

	linker := &fakeLinker{}
	consumer := newTestConsumer(linker, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), &kgo.Record{Topic: events.TopicAuth, Value: data}, commit.commit))
	require.Empty(t, linker.cmds)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_Malformed_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	linker := &fakeLinker{}
	consumer := newTestConsumer(linker, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), &kgo.Record{Topic: events.TopicCustomer, Value: []byte{0xff, 0xff}}, commit.commit))
	require.Empty(t, linker.cmds)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_TransientError_RetriesThenCommits(t *testing.T) {
	t.Parallel()

	linker := &fakeLinker{errs: []error{errors.New("db down")}}
	consumer := newTestConsumer(linker, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), profileCreatedRecord(t, "evt-1"), commit.commit))
	require.Len(t, linker.cmds, 2, "the failed delivery is retried")
	require.Len(t, commit.records, 1, "only the successful attempt advances the offset")
}

func TestConsumer_Handle_NotFound_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	linker := &fakeLinker{errs: []error{errs.ErrNotFound}}
	consumer := newTestConsumer(linker, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), profileCreatedRecord(t, "evt-1"), commit.commit))
	require.Len(t, linker.cmds, 1)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_CommitError_ReturnsError(t *testing.T) {
	t.Parallel()

	consumer := newTestConsumer(&fakeLinker{}, immediateClock{})
	commit := &commitRecorder{err: errors.New("commit failed")}

	err := consumer.handle(context.Background(), profileCreatedRecord(t, "evt-1"), commit.commit)
	require.Error(t, err)
}

func TestConsumer_Handle_CancelledContext_NoCommit(t *testing.T) {
	t.Parallel()

	linker := &fakeLinker{errs: []error{errors.New("db down"), errors.New("db down")}}
	consumer := newTestConsumer(linker, clock.Fixed{T: time.Now().UTC()})
	commit := &commitRecorder{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := consumer.handle(ctx, profileCreatedRecord(t, "evt-1"), commit.commit)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, commit.records)
}
