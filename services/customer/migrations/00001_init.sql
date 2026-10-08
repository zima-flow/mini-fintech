-- +goose Up
CREATE TABLE customers (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL UNIQUE,
    full_name     text        NOT NULL DEFAULT '',
    date_of_birth date,
    address       text        NOT NULL DEFAULT '',
    phone         text        NOT NULL DEFAULT '',
    citizenship   char(2),
    status        text        NOT NULL CHECK (status IN ('NEW', 'PROFILE_FILLED', 'ON_KYC', 'ACTIVE', 'REJECTED', 'BLOCKED')),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);

CREATE INDEX customers_status_idx ON customers (status);

CREATE TABLE idempotency_keys (
    scope        text        NOT NULL,
    key          text        NOT NULL CHECK (char_length(key) BETWEEN 1 AND 255),
    request_hash bytea       NOT NULL,
    response     bytea,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (scope, key)
);

CREATE TABLE outbox (
    id           uuid        PRIMARY KEY,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '{}',
    payload      bytea       NOT NULL,
    occurred_at  timestamptz NOT NULL,
    published_at timestamptz
);
CREATE INDEX outbox_unpublished_idx ON outbox (occurred_at) WHERE published_at IS NULL;

CREATE TABLE processed_events (
    event_id     uuid        PRIMARY KEY,
    event_type   text        NOT NULL,
    processed_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE processed_events;
DROP TABLE outbox;
DROP TABLE idempotency_keys;
DROP INDEX customers_status_idx;
DROP TABLE customers;
