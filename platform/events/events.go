package events

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

const (
	TopicAuth     = "auth.events"
	TopicCustomer = "customer.events"
)

const (
	TypeUserRegistered = "auth.user_registered"
	TypeProfileCreated = "customer.profile_created"
	TypeProfileFilled  = "customer.profile_filled"
)

const eventVersion = 1

func Envelope(eventID, eventType string, at time.Time, payload proto.Message) (*eventsv1.EventEnvelope, error) {
	env := &eventsv1.EventEnvelope{
		EventId:      eventID,
		EventType:    eventType,
		EventVersion: eventVersion,
		OccurredAt:   timestamppb.New(at.UTC()),
	}

	switch eventType {
	case TypeUserRegistered:
		p, ok := payload.(*eventsv1.UserRegistered)
		if !ok {
			return nil, payloadMismatchError(eventType, payload)
		}
		env.Payload = &eventsv1.EventEnvelope_UserRegistered{UserRegistered: p}
	case TypeProfileCreated:
		p, ok := payload.(*eventsv1.ProfileCreated)
		if !ok {
			return nil, payloadMismatchError(eventType, payload)
		}
		env.Payload = &eventsv1.EventEnvelope_ProfileCreated{ProfileCreated: p}
	case TypeProfileFilled:
		p, ok := payload.(*eventsv1.ProfileFilled)
		if !ok {
			return nil, payloadMismatchError(eventType, payload)
		}
		env.Payload = &eventsv1.EventEnvelope_ProfileFilled{ProfileFilled: p}
	default:
		return nil, fmt.Errorf("events: unknown event type %q: %w", eventType, errs.ErrInvalidArgument)
	}

	return env, nil
}

func Encode(env *eventsv1.EventEnvelope) ([]byte, error) {
	data, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("events: encode: %w", err)
	}
	return data, nil
}

func Decode(data []byte) (*eventsv1.EventEnvelope, error) {
	env := &eventsv1.EventEnvelope{}
	if err := proto.Unmarshal(data, env); err != nil {
		return nil, fmt.Errorf("events: decode: %w", err)
	}
	return env, nil
}

func payloadMismatchError(eventType string, payload proto.Message) error {
	return fmt.Errorf("events: payload %T does not match event type %q: %w", payload, eventType, errs.ErrInvalidArgument)
}
