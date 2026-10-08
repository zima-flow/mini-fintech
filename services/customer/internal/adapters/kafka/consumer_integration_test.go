//go:build integration

package kafkaadapter_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/migrate"
	platformoutbox "github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	kafkaadapter "github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/kafka"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/migrations"
)

const (
	traceparentHeader = "traceparent"
	requestIDHeader   = "x-request-id"
	traceparentValue  = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

var (
	kafkaContainer testcontainers.Container
	kafkaBrokers   []string
	testPool       *pgxpool.Pool
)

func TestMain(m *testing.M) {
	code, err := runIntegration(context.Background(), m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kafka integration setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runIntegration(ctx context.Context, m *testing.M) (int, error) {
	kc, brokers, err := startKafka(ctx)
	if err != nil {
		return 0, err
	}
	kafkaContainer, kafkaBrokers = kc, brokers
	defer func() { _ = kc.Terminate(context.Background()) }()

	pc, pool, err := startPostgres(ctx)
	if err != nil {
		return 0, err
	}
	testPool = pool
	defer func() {
		pool.Close()
		_ = pc.Terminate(context.Background())
	}()

	return m.Run(), nil
}

func startKafka(ctx context.Context) (testcontainers.Container, []string, error) {
	hostPort, err := freePort()
	if err != nil {
		return nil, nil, err
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "apache/kafka:3.8.0",
			ExposedPorts: []string{"9094/tcp"},
			Env: map[string]string{
				"KAFKA_NODE_ID":                          "1",
				"KAFKA_PROCESS_ROLES":                    "broker,controller",
				"KAFKA_LISTENERS":                        "PLAINTEXT://:9092,CONTROLLER://:9093,EXTERNAL://:9094",
				"KAFKA_ADVERTISED_LISTENERS":             "PLAINTEXT://localhost:9092,EXTERNAL://127.0.0.1:" + hostPort,
				"KAFKA_INTER_BROKER_LISTENER_NAME":       "PLAINTEXT",
				"KAFKA_CONTROLLER_QUORUM_VOTERS":         "1@localhost:9093",
				"KAFKA_CONTROLLER_LISTENER_NAMES":        "CONTROLLER",
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":   "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT,EXTERNAL:PLAINTEXT",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR": "1",
				"KAFKA_AUTO_CREATE_TOPICS_ENABLE":        "true",
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.PortBindings = network.PortMap{
					network.MustParsePort("9094/tcp"): {{
						HostIP:   netip.MustParseAddr("127.0.0.1"),
						HostPort: hostPort,
					}},
				}
			},
			WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start kafka: %w", err)
	}
	return ctr, []string{"127.0.0.1:" + hostPort}, nil
}

func startPostgres(ctx context.Context) (*tcpostgres.PostgresContainer, *pgxpool.Pool, error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("customer"),
		tcpostgres.WithUsername("customer_app"),
		tcpostgres.WithPassword("customer_app"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres: %w", err)
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, nil, err
	}
	pool, err := platformpostgres.NewPool(ctx, platformpostgres.Config{DSN: dsn})
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, nil, err
	}
	if err := migrate.Up(ctx, pool, migrations.FS, "."); err != nil {
		pool.Close()
		_ = ctr.Terminate(ctx)
		return nil, nil, fmt.Errorf("migrate customer: %w", err)
	}
	return ctr, pool, nil
}

func freePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	return port, err
}

func createTopic(ctx context.Context, topic string) error {
	exit, r, err := kafkaContainer.Exec(ctx, []string{
		"/opt/kafka/bin/kafka-topics.sh",
		"--bootstrap-server", "localhost:9092",
		"--create", "--if-not-exists",
		"--topic", topic,
		"--partitions", "1", "--replication-factor", "1",
	})
	if err != nil {
		return fmt.Errorf("create topic %s: %w", topic, err)
	}
	out, _ := io.ReadAll(r)
	if exit != 0 {
		return fmt.Errorf("create topic %s: exit %d: %s", topic, exit, out)
	}
	return nil
}

func uniqueTopic(prefix string) string {
	return fmt.Sprintf("%s.it.%d", prefix, time.Now().UnixNano())
}

func resetState(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`TRUNCATE customers, processed_events, outbox, idempotency_keys`)
	require.NoError(t, err)
}

func TestConsumer_ProduceConsumeDuplicateDelivery_CreatesOneProfile(t *testing.T) {
	resetState(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	topic := uniqueTopic(events.TopicAuth)
	require.NoError(t, createTopic(ctx, topic))

	produceClient, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ClientID("customer-it-producer"),
	)
	require.NoError(t, err)
	t.Cleanup(produceClient.Close)
	producer := kafkaadapter.NewProducer(produceClient)

	consumerClient, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ClientID("customer-it-consumer"),
		kgo.ConsumerGroup(uniqueTopic("customer-it-group")),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	require.NoError(t, err)
	t.Cleanup(consumerClient.Close)

	counter := &countingRegisterer{inner: app.NewRegisterProfileUseCase(
		postgres.NewTransactor(testPool), clock.Real{}, id.UUIDv7{}, nil,
	)}
	consumer := kafkaadapter.NewConsumer(kafkaadapter.ConsumerConfig{
		Client:     consumerClient,
		Registerer: counter,
		Clock:      clock.Real{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Propagator: propagation.TraceContext{},
	})

	runDone := make(chan error, 1)
	go func() { runDone <- consumer.Run(ctx) }()

	eventID := id.UUIDv7{}.New()
	userID := id.UUIDv7{}.New()
	payload := userRegisteredPayload(t, eventID, userID)
	headers := map[string]string{traceparentHeader: traceparentValue, requestIDHeader: "req-it-1"}

	require.NoError(t, producer.Publish(ctx, topic, headers, payload))
	require.Eventually(t, func() bool { return counter.calls() >= 1 }, 30*time.Second, 100*time.Millisecond,
		"the first delivery is consumed")
	require.Equal(t, 1, countCustomers(t, userID), "the first delivery creates exactly one profile")

	// At-least-once: the same event_id is delivered again and must be a no-op.
	require.NoError(t, producer.Publish(ctx, topic, headers, payload))
	require.Eventually(t, func() bool { return counter.calls() >= 2 }, 30*time.Second, 100*time.Millisecond,
		"the duplicate is consumed")

	require.Equal(t, 1, countCustomers(t, userID), "a duplicate delivery creates no second profile")
	require.Equal(t, 1, countProcessed(t, eventID), "the event is marked processed once")
	require.Equal(t, 1, countProfileCreatedEvents(t), "profile_created is emitted exactly once")
	require.Equal(t, traceparentValue, profileCreatedHeaders(t)[traceparentHeader],
		"the consumed trace is carried onto the emitted fact (task 5.3.1)")

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not stop after cancellation")
	}
}

func TestOutboxRelay_PublishesTraceHeadersThroughBroker(t *testing.T) {
	resetState(t)
	ctx := context.Background()

	topic := uniqueTopic(events.TopicAuth)
	require.NoError(t, createTopic(ctx, topic))

	eventID := id.UUIDv7{}.New()
	userID := id.UUIDv7{}.New()
	payload := userRegisteredPayload(t, eventID, userID)
	headers := map[string]string{traceparentHeader: traceparentValue, requestIDHeader: "req-it-trace"}

	tx := postgres.NewTransactor(testPool)
	require.NoError(t, tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		return uow.Outbox().Enqueue(ctx, domain.OutboxMessage{
			ID:         eventID,
			Topic:      topic,
			EventType:  events.TypeUserRegistered,
			Headers:    headers,
			OccurredAt: time.Now().UTC(),
			Payload:    payload,
		})
	}))

	produceClient, err := kgo.NewClient(kgo.SeedBrokers(kafkaBrokers...), kgo.ClientID("customer-it-relay"))
	require.NoError(t, err)
	t.Cleanup(produceClient.Close)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	propagator := propagation.TraceContext{}

	relay := platformoutbox.NewRelay(platformoutbox.RelayConfig{
		DB:         testPool,
		Publisher:  kafkaadapter.NewProducer(produceClient),
		Clock:      clock.Real{},
		Tracer:     provider.Tracer("test"),
		Propagator: propagator,
	})
	published, err := relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, published, "the seeded outbox row is published")

	reader, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ConsumerGroup(uniqueTopic("customer-it-trace")),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(reader.Close)

	record := pollForEvent(t, ctx, reader, eventID)
	require.Equal(t, "req-it-trace", headerValue(record, requestIDHeader), "non-trace headers survive the hop")

	carrier := propagation.MapCarrier{}
	for _, header := range record.Headers {
		carrier[string(header.Key)] = string(header.Value)
	}
	recordContext := trace.SpanContextFromContext(propagator.Extract(context.Background(), carrier))
	require.True(t, recordContext.IsValid())
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", recordContext.TraceID().String(),
		"the stored trace id is preserved across the real broker")
	require.NotEqual(t, "00f067aa0ba902b7", recordContext.SpanID().String(),
		"the record carries the relay's producer span")

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, trace.SpanKindProducer, ended[0].SpanKind())
	require.Equal(t, recordContext.SpanID(), ended[0].SpanContext().SpanID())
}

type countingRegisterer struct {
	inner kafkaadapter.Registerer
	mu    sync.Mutex
	n     int
}

func (r *countingRegisterer) RegisterProfile(ctx context.Context, cmd app.RegisterProfileCommand) error {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	return r.inner.RegisterProfile(ctx, cmd)
}

func (r *countingRegisterer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func userRegisteredPayload(t *testing.T, eventID, userID string) []byte {
	t.Helper()

	envelope, err := events.Envelope(eventID, events.TypeUserRegistered, time.Now().UTC(), &eventsv1.UserRegistered{
		UserId: userID,
		Email:  "integration@example.com",
		Role:   "CLIENT",
	})
	require.NoError(t, err)
	payload, err := events.Encode(envelope)
	require.NoError(t, err)
	return payload
}

func pollForEvent(t *testing.T, ctx context.Context, reader *kgo.Client, eventID string) *kgo.Record {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		pollCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		fetches := reader.PollFetches(pollCtx)
		cancel()

		var found *kgo.Record
		fetches.EachRecord(func(record *kgo.Record) {
			if found != nil {
				return
			}
			envelope, err := events.Decode(record.Value)
			if err == nil && envelope.GetEventId() == eventID {
				found = record
			}
		})
		if err := fetches.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			require.NoError(t, err)
		}
		if found != nil {
			return found
		}
	}
	t.Fatalf("event %s was not consumed within the deadline", eventID)
	return nil
}

func headerValue(record *kgo.Record, key string) string {
	for _, header := range record.Headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func countCustomers(t *testing.T, userID string) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM customers WHERE user_id = $1`, userID).Scan(&n))
	return n
}

func countProcessed(t *testing.T, eventID string) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM processed_events WHERE event_id = $1`, eventID).Scan(&n))
	return n
}

func countProfileCreatedEvents(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox WHERE event_type = $1`, events.TypeProfileCreated).Scan(&n))
	return n
}

func profileCreatedHeaders(t *testing.T) map[string]string {
	t.Helper()
	var headers map[string]string
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT headers FROM outbox WHERE event_type = $1`, events.TypeProfileCreated).Scan(&headers))
	return headers
}
