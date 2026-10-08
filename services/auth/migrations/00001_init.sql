-- +goose Up

CREATE TABLE users (
    id            uuid        PRIMARY KEY,
    email         text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    role          text        NOT NULL CHECK (role IN ('CLIENT', 'OFFICER', 'ADMIN')),
    customer_id   uuid,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);

CREATE TABLE refresh_tokens (
    id             uuid        PRIMARY KEY,
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash     bytea       NOT NULL UNIQUE,
    family_id      uuid        NOT NULL,
    issued_at      timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    rotated_at     timestamptz,
    replaced_by_id uuid,
    revoked_at     timestamptz,
    revoked_reason text
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);

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
DROP TABLE refresh_tokens;
DROP TABLE users;
