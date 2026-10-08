#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE=(docker compose -f deploy/docker-compose.yml --env-file .env)

AUTH_GRPC_PORT=50051
AUTH_HTTP_PORT=8081
CUSTOMER_GRPC_PORT=50052
GATEWAY_HTTP_PORT=8080
AUTH_ADDR="localhost:${AUTH_GRPC_PORT}"
CUSTOMER_ADDR="localhost:${CUSTOMER_GRPC_PORT}"
GATEWAY_ADDR="http://localhost:${GATEWAY_HTTP_PORT}"
AUTH_JWKS_ADDR="http://127.0.0.1:${AUTH_HTTP_PORT}/.well-known/jwks.json"

AUTH_DB="postgres://auth_app:auth_app@localhost:5432/auth?sslmode=disable"
CUSTOMER_DB="postgres://customer_app:customer_app@localhost:5432/customer?sslmode=disable"
AUTH_KEY="deploy/keys/auth_ed25519_private.pem"
KAFKA_ADDR="localhost:9092"

BIN_DIR="$(mktemp -d)"
AUTH_BIN="$BIN_DIR/auth"
CUSTOMER_BIN="$BIN_DIR/customer"
GATEWAY_BIN="$BIN_DIR/api-gateway"
AUTH_LOG="$BIN_DIR/auth.log"
CUSTOMER_LOG="$BIN_DIR/customer.log"
GATEWAY_LOG="$BIN_DIR/gateway.log"

PIDS=()

die() {
  echo "FAIL  $*" >&2
  exit 1
}

dump_logs() {
  echo "---- auth.log ----" >&2
  cat "$AUTH_LOG" >&2 2>/dev/null || true
  echo "---- customer.log ----" >&2
  cat "$CUSTOMER_LOG" >&2 2>/dev/null || true
  echo "---- gateway.log ----" >&2
  cat "$GATEWAY_LOG" >&2 2>/dev/null || true
}

require() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

processes_alive() {
  local pid
  for pid in ${PIDS[@]+"${PIDS[@]}"}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      dump_logs
      die "a service exited unexpectedly (pid $pid)"
    fi
  done
}

teardown() {
  local status=$?
  local pid
  for pid in ${PIDS[@]+"${PIDS[@]}"}; do
    if kill -0 "$pid" 2>/dev/null; then
      echo "==> stopping service (SIGTERM, pid $pid)"
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in ${PIDS[@]+"${PIDS[@]}"}; do
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$BIN_DIR"
  echo "==> tearing the stack down (make down)"
  make down >/dev/null 2>&1 || true
  exit "$status"
}
trap teardown EXIT

require jq
require curl
require go
require openssl

if [[ ! -f .env ]]; then
  echo "==> .env missing; copying .env.example"
  cp .env.example .env
fi

echo "==> bringing the stack up (make up)"
make up

echo "==> ensuring Kafka topics exist"
for topic in auth.events customer.events; do
  if "${COMPOSE[@]}" exec -T kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists \
    --topic "$topic" --partitions 1 --replication-factor 1 >/dev/null 2>&1; then
    echo "ok    topic $topic"
  else
    die "could not create topic $topic"
  fi
done

echo "==> generating the auth dev key (make auth-keygen)"
make auth-keygen >/dev/null

echo "==> building services"
go build -o "$AUTH_BIN" ./services/auth/cmd/auth
go build -o "$CUSTOMER_BIN" ./services/customer/cmd/customer
go build -o "$GATEWAY_BIN" ./services/api-gateway/cmd/api-gateway

echo "==> applying migrations"
env DATABASE_URL="$AUTH_DB" "$AUTH_BIN" -migrate
env DATABASE_URL="$CUSTOMER_DB" "$CUSTOMER_BIN" -migrate

echo "==> starting auth (gRPC :$AUTH_GRPC_PORT, JWKS :$AUTH_HTTP_PORT)"
env DATABASE_URL="$AUTH_DB" GRPC_ADDR=":${AUTH_GRPC_PORT}" HTTP_ADDR=":${AUTH_HTTP_PORT}" \
  AUTH_DEV=true AUTH_JWT_KEY_PATH="$AUTH_KEY" KAFKA_BROKERS="$KAFKA_ADDR" \
  "$AUTH_BIN" >"$AUTH_LOG" 2>&1 &
PIDS+=("$!")

echo "==> starting customer (gRPC :$CUSTOMER_GRPC_PORT)"
env DATABASE_URL="$CUSTOMER_DB" GRPC_ADDR=":${CUSTOMER_GRPC_PORT}" \
  AUTH_DEV=true KAFKA_BROKERS="$KAFKA_ADDR" KAFKA_GROUP=customer-service \
  "$CUSTOMER_BIN" >"$CUSTOMER_LOG" 2>&1 &
PIDS+=("$!")

echo "==> starting api-gateway (HTTP :$GATEWAY_HTTP_PORT)"
env HTTP_ADDR=":${GATEWAY_HTTP_PORT}" AUTH_DEV=true \
  AUTH_GRPC_ADDR="$AUTH_ADDR" CUSTOMER_GRPC_ADDR="$CUSTOMER_ADDR" \
  "$GATEWAY_BIN" >"$GATEWAY_LOG" 2>&1 &
PIDS+=("$!")

echo "==> waiting for auth readiness (JWKS)"
jwks_ok=0
for _ in $(seq 1 100); do
  if curl -sf "$AUTH_JWKS_ADDR" >/dev/null 2>&1; then
    jwks_ok=1
    break
  fi
  processes_alive
  sleep 0.2
done
((jwks_ok == 1)) || {
  dump_logs
  die "auth JWKS did not become ready"
}
echo "ok    auth JWKS is serving"

echo "==> waiting for api-gateway readiness (auth + customer + Redis)"
ready_ok=0
for _ in $(seq 1 100); do
  if curl -sf "$GATEWAY_ADDR/readyz" >/dev/null 2>&1; then
    ready_ok=1
    break
  fi
  processes_alive
  sleep 0.2
done
((ready_ok == 1)) || {
  dump_logs
  die "api-gateway /readyz never returned 200"
}
echo "ok    gateway /readyz is 200"

# REST flow

RUN_ID="$(date +%s%N)"
EMAIL="e2e-${RUN_ID}@example.com"
PASSWORD="Str0ng-Passphrase-2026!"
IDEMPOTENCY_REGISTER="e2e-register-${RUN_ID}"

echo "==> POST /v1/auth/register ($EMAIL)"
register_body="$(jq -nc --arg e "$EMAIL" --arg p "$PASSWORD" '{email:$e,password:$p}')"
register_resp="$(curl -sS -X POST "$GATEWAY_ADDR/v1/auth/register" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEMPOTENCY_REGISTER" \
  -d "$register_body")"
USER_ID="$(jq -er '.user_id' <<<"$register_resp")" || die "register response: $register_resp"
ROLE="$(jq -er '.role' <<<"$register_resp")" || die "register response: $register_resp"
[[ "$ROLE" == "ROLE_CLIENT" ]] || die "expected ROLE_CLIENT, got $ROLE"
echo "ok    user_id=$USER_ID role=$ROLE"

echo "==> POST /v1/auth/register (replay, same Idempotency-Key)"
replay_resp="$(curl -sS -X POST "$GATEWAY_ADDR/v1/auth/register" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEMPOTENCY_REGISTER" \
  -d "$register_body")"
REPLAY_USER_ID="$(jq -er '.user_id' <<<"$replay_resp")" || die "replay response: $replay_resp"
[[ "$REPLAY_USER_ID" == "$USER_ID" ]] || die "idempotent replay returned a different user_id"
echo "ok    idempotent replay returned the same user_id"

echo "==> POST /v1/auth/login"
login_body="$(jq -nc --arg e "$EMAIL" --arg p "$PASSWORD" '{email:$e,password:$p}')"
login_resp="$(curl -sS -X POST "$GATEWAY_ADDR/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "$login_body")"
ACCESS_TOKEN="$(jq -er '.tokens.access_token' <<<"$login_resp")" || die "login response: $login_resp"
REFRESH_TOKEN="$(jq -er '.tokens.refresh_token' <<<"$login_resp")" || die "login response: $login_resp"
[[ -n "$ACCESS_TOKEN" && -n "$REFRESH_TOKEN" ]] || die "login returned an empty token pair"
echo "ok    received an access token (and a refresh token)"

echo "==> waiting for the asynchronous profile (customer consumes auth.user_registered)"
profile_ok=0
for _ in $(seq 1 150); do
  code="$(curl -sS -o "$BIN_DIR/profile.json" -w '%{http_code}' \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    "$GATEWAY_ADDR/v1/customer/profile" 2>/dev/null || true)"
  case "$code" in
  200)
    profile_ok=1
    break
    ;;
  404) : ;;
  "")
    processes_alive
    die "gateway unreachable while polling the profile"
    ;;
  *) die "unexpected profile response HTTP $code: $(cat "$BIN_DIR/profile.json")" ;;
  esac
  sleep 0.2
done
((profile_ok == 1)) || {
  dump_logs
  die "profile was not created within the timeout"
}
STATUS_BEFORE="$(jq -er '.customer.status' "$BIN_DIR/profile.json")" || die "profile payload: $(cat "$BIN_DIR/profile.json")"
echo "ok    profile exists (status=$STATUS_BEFORE)"

echo "==> PUT /v1/customer/profile"
IDEMPOTENCY_PROFILE="e2e-profile-${RUN_ID}"
profile_body="$(jq -nc '{full_name:"E2E Smoke",date_of_birth:"1990-01-02",address:"1 Test St",phone:"+15551234567",citizenship:"us"}')"
profile_resp="$(curl -sS -X PUT "$GATEWAY_ADDR/v1/customer/profile" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Idempotency-Key: $IDEMPOTENCY_PROFILE" \
  -d "$profile_body")"
STATUS_AFTER="$(jq -er '.customer.status' <<<"$profile_resp")" || die "update response: $profile_resp"
[[ "$STATUS_AFTER" == "CUSTOMER_STATUS_PROFILE_FILLED" ]] ||
  die "expected CUSTOMER_STATUS_PROFILE_FILLED, got $STATUS_AFTER"
echo "ok    status transition: $STATUS_BEFORE -> $STATUS_AFTER"

echo "==> GET /v1/customer/status"
status_resp="$(curl -sS -H "Authorization: Bearer $ACCESS_TOKEN" "$GATEWAY_ADDR/v1/customer/status")"
STATUS="$(jq -er '.status' <<<"$status_resp")" || die "status response: $status_resp"
[[ "$STATUS" == "CUSTOMER_STATUS_PROFILE_FILLED" ]] ||
  die "expected CUSTOMER_STATUS_PROFILE_FILLED, got $STATUS"
echo "ok    status=$STATUS"

echo "==> RBAC: CLIENT on an officer route must be 403"
rbac_code="$(curl -sS -o "$BIN_DIR/rbac.json" -w '%{http_code}' \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  "$GATEWAY_ADDR/v1/officers/customers" 2>/dev/null || true)"
[[ "$rbac_code" == "403" ]] || die "expected HTTP 403 on the officer route, got $rbac_code"
RBAC_ERR="$(jq -er '.code' "$BIN_DIR/rbac.json")" || die "rbac payload: $(cat "$BIN_DIR/rbac.json")"
[[ "$RBAC_ERR" == "PERMISSION_DENIED" ]] || die "expected PERMISSION_DENIED, got $RBAC_ERR"
echo "ok    officer route denied: HTTP 403 $RBAC_ERR"

echo "e2e-stage1: PASS — register -> login -> profile ($STATUS_BEFORE -> $STATUS_AFTER) -> RBAC 403"
