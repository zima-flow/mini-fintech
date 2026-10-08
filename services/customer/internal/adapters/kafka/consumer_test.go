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
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
)

type immediateClock struct{}

func (immediateClock) Now() time.Time { return time.Now().UTC() }

func (immediateClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

type fakeRegisterer struct {
	cmds []app.RegisterProfileCommand
	ctxs []context.Context
	errs []error
}

func (r *fakeRegisterer) RegisterProfile(ctx context.Context, cmd app.RegisterProfileCommand) error {
	r.cmds = append(r.cmds, cmd)
	r.ctxs = append(r.ctxs, ctx)
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
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

func newTestConsumer(registerer Registerer, clk clock.Clock) *Consumer {
	return NewConsumer(ConsumerConfig{
		Registerer: registerer,
		Clock:      clk,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Propagator: propagation.TraceContext{},
	})
}

func userRegisteredRecord(t *testing.T, eventID, userID string) *kgo.Record {
	t.Helper()

	env, err := events.Envelope(eventID, events.TypeUserRegistered, time.Now().UTC().Truncate(time.Second), &eventsv1.UserRegistered{
		UserId: userID,
		Email:  "user@example.com",
		Role:   "CLIENT",
	})
	require.NoError(t, err)
	data, err := events.Encode(env)
	require.NoError(t, err)
	return &kgo.Record{Topic: events.TopicAuth, Partition: 0, Offset: 7, Value: data}
}

func TestConsumer_Handle_UserRegistered_RegistersThenCommits(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))

	require.Len(t, registerer.cmds, 1)
	require.Equal(t, app.RegisterProfileCommand{
		EventID:   "evt-1",
		EventType: events.TypeUserRegistered,
		UserID:    "user-1",
	}, registerer.cmds[0])
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_ExtractsTraceContext(t *testing.T) {
	t.Parallel()

	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	registerer := &fakeRegisterer{}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	record := userRegisteredRecord(t, "evt-trace", "user-1")
	record.Headers = []kgo.RecordHeader{{Key: "traceparent", Value: []byte(traceparent)}}

	require.NoError(t, consumer.handle(context.Background(), record, commit.commit))

	require.Len(t, registerer.cmds, 1)
	require.Equal(t, traceparent, registerer.cmds[0].Headers["traceparent"])
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", trace.SpanContextFromContext(registerer.ctxs[0]).TraceID().String())
}

func TestConsumer_Handle_StartsConsumerSpan(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	registerer := &fakeRegisterer{}
	consumer := NewConsumer(ConsumerConfig{
		Registerer: registerer,
		Clock:      immediateClock{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Propagator: propagation.TraceContext{},
		Tracer:     provider.Tracer("test"),
	})
	commit := &commitRecorder{}

	record := userRegisteredRecord(t, "evt-trace", "user-1")
	record.Headers = []kgo.RecordHeader{{Key: "traceparent", Value: []byte(traceparent)}}

	require.NoError(t, consumer.handle(context.Background(), record, commit.commit))

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, trace.SpanKindConsumer, ended[0].SpanKind())
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", ended[0].SpanContext().TraceID().String())
	require.Equal(t, "00f067aa0ba902b7", ended[0].Parent().SpanID().String(), "the consumer span continues the producer")
	require.Len(t, registerer.ctxs, 1)
	require.Equal(t, ended[0].SpanContext().SpanID(), trace.SpanContextFromContext(registerer.ctxs[0]).SpanID(),
		"the handler must run inside the consumer span")
}

func TestConsumer_Handle_DuplicateDelivery_CommitsBoth(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))
	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))

	require.Len(t, registerer.cmds, 2)
	require.Len(t, commit.records, 2)
}

func TestConsumer_Handle_ForeignEvent_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	env, err := events.Envelope("evt-2", events.TypeProfileCreated, time.Now().UTC(), &eventsv1.ProfileCreated{
		UserId: "user-1", CustomerId: "cust-1",
	})
	require.NoError(t, err)
	data, err := events.Encode(env)
	require.NoError(t, err)

	registerer := &fakeRegisterer{}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), &kgo.Record{Topic: events.TopicAuth, Value: data}, commit.commit))
	require.Empty(t, registerer.cmds)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_Malformed_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), &kgo.Record{Topic: events.TopicAuth, Value: []byte{0xff, 0xff}}, commit.commit))
	require.Empty(t, registerer.cmds)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_TransientError_RetriesThenCommits(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{errs: []error{errors.New("db down")}}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))
	require.Len(t, registerer.cmds, 2, "the failed delivery is retried")
	require.Len(t, commit.records, 1, "only the successful attempt advances the offset")
}

func TestConsumer_Handle_AlreadyExists_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{errs: []error{errs.ErrAlreadyExists}}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))
	require.Len(t, registerer.cmds, 1)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_InvalidArgument_SkipsAndCommits(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{errs: []error{errs.ErrInvalidArgument}}
	consumer := newTestConsumer(registerer, immediateClock{})
	commit := &commitRecorder{}

	require.NoError(t, consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit))
	require.Len(t, registerer.cmds, 1)
	require.Len(t, commit.records, 1)
}

func TestConsumer_Handle_CommitError_ReturnsError(t *testing.T) {
	t.Parallel()

	consumer := newTestConsumer(&fakeRegisterer{}, immediateClock{})
	commit := &commitRecorder{err: errors.New("commit failed")}

	err := consumer.handle(context.Background(), userRegisteredRecord(t, "evt-1", "user-1"), commit.commit)
	require.Error(t, err)
}

func TestConsumer_Handle_CancelledContext_NoCommit(t *testing.T) {
	t.Parallel()

	registerer := &fakeRegisterer{errs: []error{errors.New("db down"), errors.New("db down")}}
	consumer := newTestConsumer(registerer, clock.Fixed{T: time.Now().UTC()})
	commit := &commitRecorder{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := consumer.handle(ctx, userRegisteredRecord(t, "evt-1", "user-1"), commit.commit)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, commit.records)
}
