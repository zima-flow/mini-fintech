#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE=(docker compose -f deploy/docker-compose.yml --env-file .env)

SERVICES=(postgres redis kafka minio jaeger otel-collector prometheus grafana)

EXAMPLE_ADDR="localhost:50059"
export DATABASE_URL="${DATABASE_URL:-postgres://example_app:example_app@localhost:5432/example?sslmode=disable}"
export GRPC_ADDR="${GRPC_ADDR:-:50059}"
export OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-localhost:4317}"
export AUTH_DEV="${AUTH_DEV:-true}"

BIN_DIR="$(mktemp -d)"
EXAMPLE_BIN="$BIN_DIR/example"
EXAMPLE_LOG="$BIN_DIR/example.log"
EXAMPLE_PID=""

teardown() {
  local status=$?
  if [[ -n "$EXAMPLE_PID" ]] && kill -0 "$EXAMPLE_PID" 2>/dev/null; then
    echo "==> stopping services/example (SIGTERM)"
    kill -TERM "$EXAMPLE_PID" 2>/dev/null || true
    wait "$EXAMPLE_PID" 2>/dev/null || true
  fi
  rm -rf "$BIN_DIR"
  echo "==> tearing the stack down"
  make down || true
  exit "$status"
}
trap teardown EXIT

GRPCURL="$(command -v grpcurl || true)"
if [[ -z "$GRPCURL" && -x "$(go env GOPATH)/bin/grpcurl" ]]; then
  GRPCURL="$(go env GOPATH)/bin/grpcurl"
fi
if [[ -z "$GRPCURL" ]]; then
  echo "==> installing grpcurl v1.9.3"
  GOBIN="$(go env GOPATH)/bin" go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.3
  GRPCURL="$(go env GOPATH)/bin/grpcurl"
fi

if [[ ! -f .env ]]; then
  echo "==> .env missing; copying .env.example"
  cp .env.example .env
fi

echo "==> bringing the stack up (make up)"
make up

echo "==> asserting container health"
failures=0
for svc in "${SERVICES[@]}"; do
  container="$("${COMPOSE[@]}" ps -q "$svc")"
  if [[ -z "$container" ]]; then
    echo "FAIL  $svc: no container" >&2
    failures=$((failures + 1))
    continue
  fi
  health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container")"
  if [[ "$health" == "healthy" ]]; then
    echo "ok    $svc: $health"
  else
    echo "FAIL  $svc: $health" >&2
    failures=$((failures + 1))
  fi
done

if ((failures > 0)); then
  echo "e2e: $failures service(s) not healthy" >&2
  exit 1
fi
echo "e2e: infrastructure is healthy"

echo "==> building services/example"
go build -o "$EXAMPLE_BIN" ./services/example/cmd/example

echo "==> applying services/example migrations"
"$EXAMPLE_BIN" -migrate

echo "==> starting services/example on $GRPC_ADDR"
"$EXAMPLE_BIN" >"$EXAMPLE_LOG" 2>&1 &
EXAMPLE_PID=$!

echo "==> probing grpc.health.v1"
health_ok=0
for _ in $(seq 1 50); do
  if "$GRPCURL" -plaintext -d '{}' "$EXAMPLE_ADDR" grpc.health.v1.Health/Check 2>/dev/null | grep -q SERVING; then
    health_ok=1
    break
  fi
  if ! kill -0 "$EXAMPLE_PID" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if ((health_ok == 0)); then
  echo "FAIL  services/example health" >&2
  cat "$EXAMPLE_LOG" >&2
  exit 1
fi
echo "ok    services/example health: SERVING"

echo "==> Echo round-trip"
MESSAGE="e2e-$(date +%s%N)"
RESPONSE="$("$GRPCURL" -plaintext -d "{\"message\":\"$MESSAGE\"}" "$EXAMPLE_ADDR" bank.example.v1.ExampleService/Echo)"
if grep -q "$MESSAGE" <<<"$RESPONSE"; then
  echo "ok    Echo returned the message"
else
  echo "FAIL  Echo response was: $RESPONSE" >&2
  cat "$EXAMPLE_LOG" >&2
  exit 1
fi

echo "e2e: infrastructure healthy; services/example health + Echo OK"
