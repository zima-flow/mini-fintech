package postgres

import (
	"context"
	"fmt"
	"time"

	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type ProcessedEventsRepo struct {
	db platformpostgres.DBTX
}

var _ domain.ProcessedEventsRepo = (*ProcessedEventsRepo)(nil)

func NewProcessedEventsRepo(db platformpostgres.DBTX) *ProcessedEventsRepo {
	return &ProcessedEventsRepo{db: db}
}

const markProcessedSQL = `INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`

func (r *ProcessedEventsRepo) MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	pgID, err := toPgUUID(eventID)
	if err != nil {
		return false, err
	}
	tag, err := r.db.Exec(ctx, markProcessedSQL, pgID, eventType, at.UTC())
	if err != nil {
		return false, fmt.Errorf("mark processed %s: %w", eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}
