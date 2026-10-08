package events_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
)

func TestEventTypeAndTopicConstants(t *testing.T) {
	t.Parallel()
	require.Equal(t, "auth.events", events.TopicAuth)
	require.Equal(t, "customer.events", events.TopicCustomer)
	require.Equal(t, "auth.user_registered", events.TypeUserRegistered)
	require.Equal(t, "customer.profile_created", events.TypeProfileCreated)
	require.Equal(t, "customer.profile_filled", events.TypeProfileFilled)
}

func TestEnvelope_RoundTripConcreteOneofCases(t *testing.T) {
	t.Parallel()
	const eventID = "0199c0de-0000-7000-8000-000000000000"
	occurredAt := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		eventType string
		payload   proto.Message
		check     func(t *testing.T, env *eventsv1.EventEnvelope)
	}{
		"user_registered": {
			eventType: events.TypeUserRegistered,
			payload: &eventsv1.UserRegistered{
				UserId: "user-1",
				Email:  "client@example.com",
				Role:   "CLIENT",
			},
			check: func(t *testing.T, env *eventsv1.EventEnvelope) {
				t.Helper()
				got, ok := env.GetPayload().(*eventsv1.EventEnvelope_UserRegistered)
				require.True(t, ok, "payload must use the concrete user_registered case, not raw")
				require.Equal(t, "user-1", got.UserRegistered.GetUserId())
				require.Equal(t, "client@example.com", got.UserRegistered.GetEmail())
				require.Equal(t, "CLIENT", got.UserRegistered.GetRole())
			},
		},
		"profile_created": {
			eventType: events.TypeProfileCreated,
			payload: &eventsv1.ProfileCreated{
				UserId:     "user-1",
				CustomerId: "customer-1",
			},
			check: func(t *testing.T, env *eventsv1.EventEnvelope) {
				t.Helper()
				got, ok := env.GetPayload().(*eventsv1.EventEnvelope_ProfileCreated)
				require.True(t, ok, "payload must use the concrete profile_created case, not raw")
				require.Equal(t, "user-1", got.ProfileCreated.GetUserId())
				require.Equal(t, "customer-1", got.ProfileCreated.GetCustomerId())
			},
		},
		"profile_filled": {
			eventType: events.TypeProfileFilled,
			payload: &eventsv1.ProfileFilled{
				CustomerId: "customer-1",
				UserId:     "user-1",
			},
			check: func(t *testing.T, env *eventsv1.EventEnvelope) {
				t.Helper()
				got, ok := env.GetPayload().(*eventsv1.EventEnvelope_ProfileFilled)
				require.True(t, ok, "payload must use the concrete profile_filled case, not raw")
				require.Equal(t, "customer-1", got.ProfileFilled.GetCustomerId())
				require.Equal(t, "user-1", got.ProfileFilled.GetUserId())
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env, err := events.Envelope(eventID, tc.eventType, occurredAt, tc.payload)
			require.NoError(t, err)

			require.Equal(t, eventID, env.GetEventId())
			require.Equal(t, tc.eventType, env.GetEventType())
			require.Equal(t, int32(1), env.GetEventVersion())
			require.True(t, occurredAt.Equal(env.GetOccurredAt().AsTime()))
			require.Equal(t, time.UTC, env.GetOccurredAt().AsTime().Location())

			data, err := events.Encode(env)
			require.NoError(t, err)

			decoded, err := events.Decode(data)
			require.NoError(t, err)
			require.True(t, proto.Equal(env, decoded), "decoded envelope must equal the encoded one")
			tc.check(t, decoded)
		})
	}
}

func TestEnvelope_UnknownEventType_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := events.Envelope(
		"0199c0de-0000-7000-8000-000000000000",
		"auth.mystery",
		time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC),
		&eventsv1.UserRegistered{UserId: "user-1"},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestEnvelope_PayloadDoesNotMatchEventType_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := events.Envelope(
		"0199c0de-0000-7000-8000-000000000000",
		events.TypeUserRegistered,
		time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC),
		&eventsv1.ProfileCreated{UserId: "user-1", CustomerId: "customer-1"},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
}

func TestDecode_BrokenBytes_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := events.Decode([]byte{0xff, 0xff, 0xff, 0xff})
	require.Error(t, err)
}

func TestEncode_ThenDecode_EmptyEnvelope(t *testing.T) {
	t.Parallel()
	data, err := events.Encode(&eventsv1.EventEnvelope{EventId: "e-1"})
	require.NoError(t, err)

	decoded, err := events.Decode(data)
	require.NoError(t, err)
	require.Equal(t, "e-1", decoded.GetEventId())
}
