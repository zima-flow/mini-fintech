package postgres

import (
	"context"
	"fmt"

	platformoutbox "github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type Outbox struct {
	db platformpostgres.DBTX
}

var _ domain.Outbox = (*Outbox)(nil)

func NewOutbox(db platformpostgres.DBTX) *Outbox { return &Outbox{db: db} }

func (o *Outbox) Enqueue(ctx context.Context, msg domain.OutboxMessage) error {
	if err := platformoutbox.Enqueue(ctx, o.db, platformoutbox.Message{
		ID:         msg.ID,
		Topic:      msg.Topic,
		EventType:  msg.EventType,
		Headers:    msg.Headers,
		Payload:    msg.Payload,
		OccurredAt: msg.OccurredAt,
	}); err != nil {
		return fmt.Errorf("enqueue outbox message: %w", err)
	}
	return nil
}
