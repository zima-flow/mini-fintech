-- +goose Up
CREATE TABLE echoes (
    id         uuid        PRIMARY KEY,
    message    text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE echoes;
