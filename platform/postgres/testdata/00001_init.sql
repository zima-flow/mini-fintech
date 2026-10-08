-- +goose Up
CREATE TABLE t (
    x integer NOT NULL
);

CREATE TABLE outbox (
  id           uuid PRIMARY KEY,
  topic        text        NOT NULL,
  event_type   text        NOT NULL,
  headers      jsonb       NOT NULL DEFAULT '{}',
  payload      bytea       NOT NULL,
  occurred_at  timestamptz NOT NULL,
  published_at timestamptz
);
CREATE INDEX outbox_unpublished_idx ON outbox (occurred_at) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE t;
