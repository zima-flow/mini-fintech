//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

func TestRegisterProfile_ConsumeTwice_CreatesOneProfileAndOneEvent(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	uc := app.NewRegisterProfileUseCase(postgres.NewTransactor(pool), clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)

	cmd := app.RegisterProfileCommand{
		EventID:   id.UUIDv7{}.New(),
		EventType: events.TypeUserRegistered,
		UserID:    id.UUIDv7{}.New(),
	}
	require.NoError(t, uc.RegisterProfile(ctx, cmd))
	require.NoError(t, uc.RegisterProfile(ctx, cmd), "a duplicate delivery is a no-op")

	var customers, processed, outboxRows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&customers))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&processed))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&outboxRows))
	require.Equal(t, 1, customers, "one profile per user")
	require.Equal(t, 1, processed, "the event is marked once")
	require.Equal(t, 1, outboxRows, "profile_created is emitted exactly once")

	var customerID, status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text, status FROM customers WHERE user_id = $1`, cmd.UserID,
	).Scan(&customerID, &status))
	require.Equal(t, "NEW", status)

	var topic string
	var payload []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT topic, payload FROM outbox WHERE event_type = $1`, events.TypeProfileCreated,
	).Scan(&topic, &payload))
	require.Equal(t, events.TopicCustomer, topic)

	envelope, err := events.Decode(payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeProfileCreated, envelope.GetEventType())
	require.Equal(t, cmd.UserID, envelope.GetProfileCreated().GetUserId())
	require.Equal(t, customerID, envelope.GetProfileCreated().GetCustomerId())
}

func TestRelay_PublishesProfileCreatedAndMarks(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)
	uc := app.NewRegisterProfileUseCase(postgres.NewTransactor(pool), clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)

	cmd := app.RegisterProfileCommand{
		EventID:   id.UUIDv7{}.New(),
		EventType: events.TypeUserRegistered,
		UserID:    id.UUIDv7{}.New(),
	}
	require.NoError(t, uc.RegisterProfile(ctx, cmd))

	pub := &recordingPublisher{}
	relay := outbox.NewRelay(outbox.RelayConfig{DB: pool, Publisher: pub, Clock: clock.Fixed{T: testNow()}})

	n, err := relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	delivered := pub.delivered()
	require.Len(t, delivered, 1)
	require.Equal(t, events.TopicCustomer, delivered[0].topic)

	envelope, err := events.Decode(delivered[0].payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeProfileCreated, envelope.GetEventType())
	require.Equal(t, cmd.UserID, envelope.GetProfileCreated().GetUserId())

	var unpublished int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished))
	require.Zero(t, unpublished, "a delivered message is marked published")

	n, err = relay.PublishPending(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "a marked message is not republished")
	require.Len(t, pub.delivered(), 1)
}

func TestUpdateProfile_PersistsStatusAndFields(t *testing.T) {
	ctx := context.Background()
	pool := startCustomerPostgres(t, ctx)

	register := app.NewRegisterProfileUseCase(postgres.NewTransactor(pool), clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)
	userID := id.UUIDv7{}.New()
	require.NoError(t, register.RegisterProfile(ctx, app.RegisterProfileCommand{
		EventID:   id.UUIDv7{}.New(),
		EventType: events.TypeUserRegistered,
		UserID:    userID,
	}))

	update := app.NewUpdateProfileUseCase(postgres.NewTransactor(pool), clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)
	result, err := update.UpdateProfile(ctx, app.UpdateProfileCommand{
		UserID:         userID,
		FullName:       "Jane Doe",
		DateOfBirth:    "1990-01-15",
		Address:        "1 Main St",
		Phone:          "+15551234567",
		Citizenship:    "us",
		IdempotencyKey: "upd-1",
	})
	require.NoError(t, err)
	require.Equal(t, domain.StatusProfileFilled, result.Customer.Status)
	require.Equal(t, "US", result.Customer.Citizenship, "citizenship is normalized")

	var fullName, dateOfBirth, address, phone, citizenship, status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT full_name, date_of_birth::text, address, phone, citizenship::text, status
		   FROM customers WHERE user_id = $1`, userID,
	).Scan(&fullName, &dateOfBirth, &address, &phone, &citizenship, &status))
	require.Equal(t, "Jane Doe", fullName)
	require.Equal(t, "1990-01-15", dateOfBirth)
	require.Equal(t, "1 Main St", address)
	require.Equal(t, "+15551234567", phone)
	require.Equal(t, "US", citizenship)
	require.Equal(t, "PROFILE_FILLED", status)

	filled := countEvents(t, pool, events.TypeProfileFilled)
	require.Equal(t, 1, filled, "the first complete update emits profile_filled once")

	_, err = update.UpdateProfile(ctx, app.UpdateProfileCommand{
		UserID:         userID,
		FullName:       "Jane A. Doe",
		DateOfBirth:    "1990-01-15",
		Address:        "2 Main St",
		Phone:          "+15557654321",
		Citizenship:    "US",
		IdempotencyKey: "upd-2",
	})
	require.NoError(t, err)
	require.Equal(t, 1, countEvents(t, pool, events.TypeProfileFilled), "a later edit does not re-emit")
}

func countEvents(t *testing.T, pool *pgxpool.Pool, eventType string) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox WHERE event_type = $1`, eventType,
	).Scan(&n))
	return n
}

type recordedMessage struct {
	topic   string
	headers map[string]string
	payload []byte
}

type recordingPublisher struct {
	mu       sync.Mutex
	messages []recordedMessage
}

func (p *recordingPublisher) Publish(_ context.Context, topic string, headers map[string]string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, recordedMessage{topic: topic, headers: headers, payload: payload})
	return nil
}

func (p *recordingPublisher) delivered() []recordedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedMessage(nil), p.messages...)
}
