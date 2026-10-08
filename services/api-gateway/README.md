# api-gateway

The REST edge of mini-fintech. It terminates client HTTP/JSON, validates bearer
tokens locally, applies token-bucket rate limits, shapes one error envelope, and
forwards commands/queries to the `auth` and `customer` gRPC services.

**It contains no business logic and owns no database.** Handlers validate shape,
call exactly one gRPC method, and map the result/error (TZ §5.1). Any ownership/role decision also
lives in the target service — a valid token at the gateway is *never* trusted on
its own.

## Why manual `net/http` handlers (not grpc-gateway)

TZ §5.1 leaves the REST↔gRPC mapping (grpc-gateway or manual handlers) open and
asks for the choice to be justified here:

- **HTTP/JSON stays out of `contracts/`.** grpc-gateway needs
  `google.api.http` annotations on every RPC and the `google/api` protos as a
  buf dependency; the contracts would then describe two transports. Here the
  `.proto` files stay pure gRPC and `make generate` stays lean.
- **Exact control of the edge behaviour.** The error envelope, the
  `Idempotency-Key` header rule, per-route role guards, which routes forward the
  JWT and which routes are rate-limited are all explicit, hand-written middleware
  rather than generated options.
- **Trivially testable.** Handlers are ordinary `http.Handler`s, so the whole
  surface (routing, middleware chain, envelope, rate limit) is tested with
  `httptest` and upstream fakes — no generated gateway binary.
- **The Stage-1 surface is small** (12 routes); the ceremony of annotations and
  a second generator is not yet worth it. If the surface grows, revisit this
  decision in an ADR.

JSON is **`snake_case`** (matching the proto field names): request bodies are
decoded with `protojson` (which also accepts the lowerCamelCase name) and
responses are encoded with `UseProtoNames: true`. The error envelope is the one
exception — see below.

## REST surface

Base path `/v1`:

| Method | Path | Auth | Role | Upstream (gRPC) |
|---|---|---|---|---|
| POST | `/v1/auth/register` | public | — | `AuthService.Register` |
| POST | `/v1/auth/login` | public | — | `AuthService.Login` |
| POST | `/v1/auth/refresh` | public (refresh token in body) | — | `AuthService.Refresh` |
| POST | `/v1/auth/logout` | public (refresh token in body) | — | `AuthService.Logout` |
| GET | `/v1/customer/profile` | bearer | CLIENT | `CustomerService.GetProfile` |
| PUT | `/v1/customer/profile` | bearer | CLIENT | `CustomerService.UpdateProfile` |
| GET | `/v1/customer/status` | bearer | CLIENT | `CustomerService.GetCustomerStatus` |
| GET | `/v1/officers/customers/{customer_id}` | bearer | OFFICER/ADMIN | `CustomerService.GetCustomer` |
| GET | `/v1/officers/customers?status=&page_size=&page_token=` | bearer | OFFICER/ADMIN | `CustomerService.ListCustomers` |
| POST | `/v1/admin/officers` | bearer | ADMIN | `AuthService.CreateOfficer` |
| GET | `/healthz` | public | — | process liveness |
| GET | `/readyz` | public | — | dependency readiness |

- **`Idempotency-Key` is required** on `POST /v1/auth/register`,
  `PUT /v1/customer/profile` and `POST /v1/admin/officers`; the value is
  forwarded as the proto `idempotency_key`.
- The authenticated routes read the verified principal and forward the raw
  `Authorization` header downstream; `GetStatus` also forwards the token's
  `customer_id` claim when present (the service resolves the owner from `sub`
  and ignores it).
- Request bodies are bounded (1 MiB) and unknown JSON fields are rejected
  (`INVALID_ARGUMENT`, 400) before any upstream call.
- **Profile creation is asynchronous**: `customer` consumes
  `auth.user_registered` over Kafka, so `GET /v1/customer/profile` may briefly
  return `404 NOT_FOUND` right after `register`/`login`. The e2e smoke script
  polls.

## Error envelope

Every error (gateway or upstream) is one shape:

```json
{ "code": "FAILED_PRECONDITION", "message": "profile is locked", "details": [], "request_id": "01a0…" }
```

`code` is the gRPC code name, `message` is safe sentinel text, `details` is
reserved (`[]` in Stage 1), `request_id` echoes the request id. One mapping
table, used only here:

| gRPC code | HTTP |
|---|---|
| `OK` | 200 |
| `INVALID_ARGUMENT` | 400 |
| `UNAUTHENTICATED` | 401 |
| `PERMISSION_DENIED` | 403 |
| `NOT_FOUND` | 404 |
| `ALREADY_EXISTS` | 409 |
| `FAILED_PRECONDITION` | 409 |
| `RESOURCE_EXHAUSTED` | 429 |
| `UNIMPLEMENTED` | 501 |
| `UNAVAILABLE` | 503 |
| `INTERNAL` / unknown | 500 |

Internal error strings and stack traces never reach the client (unknown errors
collapse to `500 {"code":"INTERNAL","message":"internal error"}`).

## Authentication and authorization

- **Local JWT validation.** `RequireAuth` verifies the bearer token against the
  auth service JWKS (`platform/authn`): EdDSA only (the header `alg` is never
  trusted), signature/`exp`/`iss`/`aud`, cached keyset with a negative cache and
  single-flight refresh (ADR-0001 D7). A missing/invalid token is `401`; a JWKS
  outage is `503 UNAVAILABLE` so a client can tell "retry" from "bad token".
- **Forwarded identity.** On success the verified principal *and* the raw
  `Authorization` header are installed in the request context; the outbound
  `platform/grpcclient` interceptors forward `authorization`, `x-request-id`
  and the W3C `traceparent`, so each service re-validates the token.
- **Role guards are defense in depth.** `RequireRole` enforces
  CLIENT/OFFICER/ADMIN at the edge, but the target service re-checks ownership
  and role. Never trust "the gateway already checked".

## Rate limiting

One atomic Lua token bucket over Redis (`go-redis`), with `now` taken from the
Redis server clock so replicas share one window.

- Authenticated routes: key `rl:user:<sub>`, default **10 tokens/s, burst 20**.
- Public auth routes: key `rl:ip:<ip>:<route>`, default **0.1 tokens/s (≈6/min),
  burst 5** — a login brute-force brake. The client IP comes from `RemoteAddr`;
  `X-Forwarded-For` is deliberately ignored so a caller cannot pick its own
  bucket (resolve the real client at the proxy first).
- On limit: `429` + `Retry-After` + the `RESOURCE_EXHAUSTED` envelope.
- **Fail-open.** A Redis error lets the request proceed with a `Warn` log and
  the `gateway_ratelimit_redis_errors_total` counter — rate limiting is not the
  correctness control (idempotency + DB constraints are).

Limits are configured as a one-token **refill interval** (the inverse of the
per-second rate), so no `float` is needed in configuration.

## Cross-cutting behaviour

- **Request id:** accepts/echoes `x-request-id`, otherwise generates UUID v7 and
  returns it in the `X-Request-Id` header; the same id flows to gRPC.
- **Tracing:** extracts the W3C trace context, starts an OTel server span, and
  records the response status; one trace spans REST → gRPC.
- **Access log:** one structured `slog` line per request with `method`, `path`,
  `status`, `duration_ms`, `request_id`, `trace_id`.
- **Recovery:** a handler panic becomes the generic `500` envelope and is
  logged; the process stays up.
- **CORS:** preflight answered with `204`; a configured origin is echoed with
  `Vary: Origin`; an empty allow-list allows any origin (local default).
- **Health:** `/healthz` reflects the process only; `/readyz` probes the auth and
  customer gRPC health services and Redis `PING` and returns `503` if any fails
  (dependency state).
- **Graceful shutdown:** bounded drain within `SHUTDOWN_TIMEOUT` on SIGTERM.

## Configuration

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `HTTP_ADDR` | no | `:8080` | REST listen address |
| `LOG_LEVEL` | no | `info` | `slog` level (`debug` → text handler) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | no | `localhost:4317` | OTLP/gRPC collector |
| `SHUTDOWN_TIMEOUT` | no | `10s` | graceful-drain bound on SIGTERM |
| `AUTH_JWKS_URL` | no | dev: `http://127.0.0.1:8081/.well-known/jwks.json` | auth service JWKS endpoint |
| `AUTH_ISSUER` / `AUTH_AUDIENCE` | no | dev: `mini-fintech-auth` / `mini-fintech` | token claims; required when `AUTH_DEV=false` |
| `AUTH_DEV` | no | `false` | relaxes the issuer/audience requirement and derives the dev JWKS URL **locally only** |
| `AUTH_GRPC_ADDR` | no | `localhost:50051` | auth gRPC target |
| `CUSTOMER_GRPC_ADDR` | no | `localhost:50052` | customer gRPC target |
| `REDIS_ADDR` | no | `localhost:6379` | Redis rate-limit store |
| `REDIS_PASSWORD` / `REDIS_DB` | no | — / `0` | Redis auth and database index |
| `RATE_LIMIT_USER_REFILL` | no | `100ms` | authenticated bucket: one token per interval (10/s) |
| `RATE_LIMIT_USER_BURST` | no | `20` | authenticated bucket capacity |
| `RATE_LIMIT_IP_REFILL` | no | `10s` | public auth bucket: one token per interval (0.1/s) |
| `RATE_LIMIT_IP_BURST` | no | `5` | public auth bucket capacity |
| `CORS_ALLOWED_ORIGINS` | no | — (any) | comma-separated origin allow-list |

## Run it locally

Requires the local infrastructure (`make up`) and the auth/customer services:

```bash
# infra
make up
# auth (JWKS on :8081, gRPC on :50051)
DATABASE_URL='postgres://auth_app:auth_app@localhost:5432/auth?sslmode=disable' \
  go run ./services/auth/cmd/auth -migrate
DATABASE_URL='postgres://auth_app:auth_app@localhost:5432/auth?sslmode=disable' \
  AUTH_DEV=true AUTH_JWT_KEY_PATH=deploy/keys/auth_ed25519_private.pem \
  go run ./services/auth/cmd/auth
# customer (gRPC on :50052)
DATABASE_URL='postgres://customer_app:customer_app@localhost:5432/customer?sslmode=disable' \
  go run ./services/customer/cmd/customer -migrate
DATABASE_URL='postgres://customer_app:customer_app@localhost:5432/customer?sslmode=disable' \
  AUTH_DEV=true go run ./services/customer/cmd/customer
# gateway
AUTH_DEV=true go run ./services/api-gateway/cmd/api-gateway
```

```bash
curl -i localhost:8080/healthz          # 200 ok
curl -i localhost:8080/readyz           # 200 ok (auth + customer health + Redis)
curl -i localhost:8080/v1/customer/profile
# 401 {"code":"UNAUTHENTICATED","message":"missing access token","details":[],"request_id":"..."}
```

`AUTH_DEV=true` is a local-only convenience: it fills the issuer/audience and
points the gateway at the auth service's default dev JWKS. **Never set it where
real tokens are accepted**.

> `AUTH_DEV=true` does the same for `auth` (its own JWKS) and `customer` (the
> auth service's dev JWKS) — every service derives the same dev identity, so a
> local run needs no explicit `AUTH_JWKS_URL`/`AUTH_ISSUER`/`AUTH_AUDIENCE`.

## Non-negotiables

- **Transport only.** No business logic, no database, no cross-service DB reads.
- **Authorize downstream too** — a valid edge token is not sufficient.
- **No floating-point money or rates**; rate limits use refill intervals.
- **One error envelope**, internal details never leak.
- **Propagate context**: `traceparent` + `x-request-id` on every downstream call.
- **Graceful shutdown** within `SHUTDOWN_TIMEOUT`.
