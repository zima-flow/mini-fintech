# Architecture

> Mini-Core — a simplified core banking system (АБС) with a KYC module, built
> as a Go microservices monorepo. This document is the map: the services, the two
> end-to-end flows, the shared substrate they are built on, and the
> simplifications we accept on purpose. Machine truth for every interface is the 
> `.proto` files under [contracts/proto](../contracts/proto).

The system serves exactly two scenarios:

1.  **Onboarding:** register → profile → upload documents → KYC review → approval
    → account auto-opened.
2.  **Money transfer:** transfer → antifraud verdict → double-entry ledger →
    notification.

Everything external (sanctions lists, SMS, document OCR) is a mock. The goal is
architecture and correctness, not integrations.

## Service map

```mermaid
**flowchart LR**
**  Client([Client]) --> GW[api-gateway]**
**  GW --> AUTH[auth]**
**  GW --> CUST[customer]**
**  GW --> DOC[document]**
**  GW --> KYC[kyc]**
**  GW --> ACC[account]**
**  GW --> LED[ledger]**
**  GW --> AF[antifraud]**
**  AUTH -. facts .-> KAFKA[(Kafka)]**
**  CUST -. facts .-> KAFKA**
**  KYC -. facts .-> KAFKA**
**  LED -. facts .-> KAFKA**
**  KAFKA -. consume .-> NOTIF[notification]**
**  ACC <--> LED**
**  KYC -->|HasRequiredDocuments| DOC**
**  LED -->|CheckTransfer| AF**
**  EXAMPLE[example]:::template**
**  classDef template fill:#eee,stroke:#999,stroke-dasharray: 4 3**
|```
|Service|Owns|Sync surface (commands/queries)|Facts published|Facts consumed|
|-|-|-|-|-|
|`api-gateway`|— (no DB, no business logic)|REST (JSON) → gRPC|—|—|
|`auth`|credentials, JWT, RBAC|`AuthService`; JWKS over HTTP (`:8081`)|`auth.user_registered`|`customer.profile_created`|
|`customer`|client profiles|`CustomerService`|`customer.profile_created`, `customer.profile_filled`|`auth.user_registered`|
|`document`|document metadata (files in MinIO)|`DocumentService`|—|—|
|`kyc`|verification applications, status machine|`KycService`|`kyc.application_*`|—|
|`account`|account lifecycle, number, status|`AccountService`|—|`kyc.application_approved`|
|`ledger`|money: entries, balances, transactions|`LedgerService`|`ledger.transaction_*`|—|
|`antifraud`|rules and verdicts|`AntifraudService`|—|`ledger.transaction_*`|
|`notification`|delivered notifications (mock)|`NotificationService` (debug only)|—|`kyc.application_approved`, `ledger.transaction_*`|
|`example`|reference template (`echoes`)|`Example`|—|—|
**Stage-1 event chain (`auth` ↔ `customer`, ADR-0003).** `auth` publishes `
auth.user_registered` on `auth.events`; `customer` consumes it (group `
customer-service`), creates an empty `NEW` profile and publishes `
customer.profile_created` on `customer.events`; `auth` consumes **that** to set `
users.customer_id`. The two services link identities by event, never by a
synchronous call. When the client completes the profile, `customer` publishes `
customer.profile_filled` on the same topic.
**Commands and queries travel over gRPC; facts travel over Kafka.** Every service
owns its own database; **no service reads another service's database or schema**
— cross-service data flow happens only through gRPC or events. Each service
follows the same hexagonal layout (`internal/{domain,app,adapters,config}` \+ `
migrations/`), with dependencies pointing inward: the domain knows nothing about
Postgres, Kafka or gRPC (rule 03).
## Onboarding flow
```mermaid
**sequenceDiagram**
**  autonumber**
**  actor C as Client**
**  participant GW as api-gateway**
**  participant AUTH as auth**
**  participant CUST as customer**
**  participant DOC as document**
**  participant KYC as kyc**
**  participant ACC as account**
**  participant KAFKA as Kafka**
**  participant MINIO as MinIO**
**  participant N as notification**

**  C->>GW: POST /register**
**  GW->>AUTH: Register(...)**
**  AUTH-->>GW: user_id + role (identity, no tokens)**
**  AUTH-->>KAFKA: auth.user_registered (outbox)**
**  KAFKA-->>CUST: user_registered**
**  CUST->>CUST: create profile**
**  Note over CUST: asynchronous — profile reads can<br/>404 briefly after register**

**  C->>GW: POST /login**
**  GW->>AUTH: Login(...)**
**  AUTH-->>GW: access + refresh tokens**

**  C->>GW: PUT /profile**
**  GW->>CUST: UpdateProfile(...)**
**  CUST-->>KAFKA: customer.profile_filled (outbox)**

**  C->>GW: POST /documents (initiate)**
**  GW->>DOC: InitiateUpload(...)**
**  DOC-->>C: presigned URL**
**  C->>MINIO: PUT file (direct)**
**  C->>GW: POST /documents/complete**
**  GW->>DOC: CompleteUpload(...)**
**  DOC->>DOC: verify magic bytes + sha256, status CONFIRMED**

**  C->>GW: POST /kyc/applications**
**  GW->>KYC: SubmitApplication(...)**
**  KYC->>DOC: HasRequiredDocuments(...)**
**  KYC->>KYC: auto-checks (sanctions/OCR mocks)**
**  Note over KYC: SUBMITTED → AUTO_CHECKS → IN_REVIEW**
**  C->>GW: (officer) POST /kyc/applications/{id}/approve**
**  GW->>KYC: Approve(...)**
**  KYC-->>KAFKA: kyc.application_approved (outbox)**
**  KAFKA-->>ACC: application_approved**
**  ACC->>ACC: OpenAccount(GEL) — first account auto-opened**
**  KAFKA-->>N: application_approved**
**  N->>N: record "verified" notification (mock send)**
```
Key invariants in this flow:
- **At most one active application per customer**, enforced by a Postgres partial
  unique index on `customer_id` over the active statuses `{SUBMITTED,
  AUTO_CHECKS, IN_REVIEW, DOCS_REQUESTED}` (ADR-0001 D6). Concurrent submissions
  resolve to one winner; the loser gets `ALREADY_EXISTS`.
- **Files never cross gRPC** — the client uploads to MinIO directly via a
  short-TTL presigned URL; the service stores only metadata (`key`, `sha256`,
  status) in its own DB.
- `kyc.application_approved`** is the hinge** where onboarding joins account
  opening: the account service consumes it and opens the customer's first
  account.
- **Every event that changes another service's state goes through the
  transactional outbox** (ADR-0001 D8) and carries an `event_id`; consumers
  dedup by it in the same transaction as their state change.
## Transfer flow
```mermaid
**sequenceDiagram**
**  autonumber**
**  actor C as Client**
**  participant GW as api-gateway**
**  participant LED as ledger**
**  participant ACC as account**
**  participant AF as antifraud**
**  participant PG as Ledger-DB**
**  participant KAFKA as Kafka**
**  participant N as notification**

**  C->>GW: POST /transfers (idempotency_key)**
**  GW->>LED: Transfer(from, to, amount, currency, key)**
**  LED->>LED: idempotency check (initiator_id, key)**
**  Note over LED: replay → return original tx (incl. DECLINED)<br/>same key + different payload → FAILED_PRECONDITION**
**  LED->>ACC: GetAccount(from), GetAccount(to)**
**  ACC-->>LED: both exist? ACTIVE? currency matches?**
**  LED->>AF: CheckTransfer(...)**
**  AF-->>LED: ALLOW | DENY**
**  alt DENY**
**    LED->>PG: transaction status DECLINED**
**    LED-->>GW: FAILED_PRECONDITION**
**  else ALLOW**
**    LED->>PG: one tx: SELECT ... FOR UPDATE (deterministic order),<br/>funds check, entries (Σdebit = Σcredit), balances, outbox**
**    LED-->>GW: Transaction COMPLETED**
**    PG-->>KAFKA: ledger.transaction_completed (outbox relay)**
**    KAFKA-->>N: notify both parties**
**    KAFKA-->>AF: accumulate daily aggregates**
**  end**
```
Key invariants in this flow:
- **Idempotency** (ADR-0001 D5): uniqueness is `(initiator_id, idempotency_key)`
  in the ledger DB — not global, not Redis. Same key + same payload returns the
  original transaction (including a `DECLINED` one); same key + different
  payload returns `FAILED_PRECONDITION`. Keys are retained permanently as part
  of the financial record.
- **Double-entry:** at least two `entries` per transaction (DEBIT/CREDIT); the
  sum of debits equals the sum of credits, verified by a test. The account
  balance is an aggregate over entries, materialized into `balances` in the same
  transaction.
- **Concurrency:** balances are locked with `SELECT ... FOR UPDATE` in a
  deterministic (`account_id`) order under READ COMMITTED, avoiding lost updates
  and deadlocks; the whole transaction is retried on `40P01`/`40001` (ADR-0002
  D4). The mandatory test is 100 concurrent transfers between two accounts —
  final balances reconcile, no negative balance, none lost or doubled.
- **Antifraud is fail-closed** (ADR-0001 D3): if the check cannot complete, the
  transfer fails with `UNAVAILABLE` and no money moves; we never silently `ALLOW`
  . `DENY` is a recorded `DECLINED` transaction; an unavailable check records
  nothing. **Open contract question:** the TZ returns `FAILED_PRECONDITION` for `
  DENY`, but rule 04 prefers modelling a business outcome as `OK` \+ `
  Transaction{DECLINED}` and reserving `FAILED_PRECONDITION` for the
  idempotency-key-reuse conflict (ADR-0001 D5). This will be settled by an ADR
  before the ledger is built.
- **Money is `int64` minor units + ISO-4217 code** — never `float`.
## The `account` ↔ `ledger` cycle (ADR-0001 D2)
The two services are mutually dependent at the network level:
- `account.GetBalance` is a thin proxy to `ledger.GetBalance` — the account
  service owns **lifecycle, number and status**, the ledger owns **money**.
- `ledger.Transfer` calls `account.GetAccount` to validate that both accounts
  exist, are `ACTIVE`, and share the currency.
That is a service-level cycle. We **accept it deliberately** (ADR-0001 D2) and
keep the responsibilities disjoint: balances are never stored in `account`;
account numbers/status are never stored in `ledger`. The two are built and
deployed **together as one slice**, and this is the **only** accepted synchronous
service-to-service cycle — adding another requires a new ADR.
This is why the TZ's "ledger owns money, account owns lifecycle" split can
still be honest while the two talk both ways: the cycle is a
lifecycle/validation dependency, not a shared state. The alternative
(duplicating balances into `account`, or routing account validation through an
event) would trade a small, bounded coupling for a consistency problem or a
distributed transaction — a worse deal at this scale.
## The substrate (Stage 0)
Stage 0 builds the shared ground every service stands on, so each later service
is a copy-and-rename of a known pattern rather than a fresh design.
- `contracts` — all `.proto` v1 definitions, packages `bank.\<domain>.v1`,
  generated by `buf` into a **committed** `contracts/gen`. One schema source for
  both gRPC and Kafka events, so there is no serialization drift; CI
  regenerates and fails on a non-empty `git diff`, proving the committed output
  is reproducible. Generated code is never hand-edited.
- `platform` — the cross-cutting concerns, injected, never global: `config`
  (fail-fast env loading), `logger` (injected `slog`), `interceptors` (the
  mandatory chain **recovery → request-id → logging → metrics → auth**, unary and
  stream), `grpcserver` (`grpc.health.v1`, readiness/liveness, bounded graceful
  drain), `otel` (OTLP/gRPC trace+metric providers plus W3C trace-context
  injection/extraction for the async hop), `grpcclient` (outbound `traceparent`
  \+ `x-request-id` \+ `authorization` propagation), `authn` (JWKS-cached EdDSA
  verifier), `events` (`bank.events.v1` envelope helpers), `postgres` (pgx v5
  pool with a per-query OTel tracer, `DBTX`, `Transactor` with READ COMMITTED
  + retry), `migrate` (goose over `//go:embed`), `outbox` (transactional outbox: `
    Enqueue` in the caller's tx + a background `Relay` that publishes with
    producer spans, trace-header injection and `outbox.published`/`outbox.lag`
    metrics), `kafka`/`redis` constructors, `clock` (UTC only, injected), `id`
    (UUID v7 behind a port), `health`.
- `deploy/` — the local infrastructure under docker-compose with pinned images:
  Postgres 16, Redis 7, Kafka 3.8 (KRaft, single node), MinIO, Jaeger, OTel
  Collector, Prometheus, Grafana. `make up` waits until every container is
  healthy; a host-port conflict fails loudly and tears down anything it
  started, so there is never a half-started stack.
- `services/example` — a runnable reference service that proves the template end
  to end: hexagonal layout, the full interceptor chain, `grpc.health.v1`, OTel
  traces/metrics, structured logging, a `Clock` port, a Postgres repository with
  a goose migration, a multi-stage Dockerfile, and graceful shutdown. A new
  service starts by copying it and renaming.
- **Tooling and CI** — one `Makefile` entrypoint (`up`, `down`, `generate`, `
  migrate`, `lint`, `test`, `test-integration`, `e2e`), golangci-lint v2 with
  the ADR-0002 dependency guards, and GitHub Actions running **lint → unit →
  integration** with a pinned Go toolchain and a clean-regeneration gate. The
  multi-module `go.work` workspace means code targets iterate the workspace
  modules rather than running root `./...`.
Why this shape: the TZ's hard constraints (`int64` money, UTC + injected `Clock`
, UUID v7, outbox, no cross-service DB access, mandatory interceptors, fail-closed antifraud) are cheap when they are encoded once in `
platform` and `contracts`, and expensive when each service re-decides them. The
example service is the executable spec of that encoding.
## The REST edge (Stage 1)
Stage 1 ships the first vertical slice — `auth`, `customer` and `api-gateway` —
running the register → login → profile flow end to end.
- `api-gateway`** is HTTP-only.** It binds a REST server on `:8080` and **no gRPC
  server**; the gRPC port `50050` from the original port convention is unused
  (Spec 001 D17). The gateway is a gRPC **client** of `auth` and `customer`.
- **Manual `net/http` handlers, not grpc-gateway** (Spec 001 D1). The `.proto`
  contracts stay pure gRPC, and the edge keeps exact control of the error
  envelope, the `Idempotency-Key` header rule, role guards and rate limits.
- **Auth serves JWKS over HTTP on `:8081`** (`GET /.well-known/jwks.json`) and
  every service verifies tokens itself against the cached keyset (ADR-0001 D7)
  — there is no per-request auth RPC. Access tokens are EdDSA/Ed25519 only.
- **Identity linking and profile creation are asynchronous** (ADR-0003, the event
  chain above). A `GET /v1/customer/profile` immediately after `register`/`login`
  can therefore return `404 NOT_FOUND` until the `auth.user_registered` event
  has been consumed; the smoke script polls.
## Deployment simplifications (conscious)
These are deliberate and are the "what I would do differently in production"
list required by the TZ. They are noted here and in `docs/STATUS.md` rather than
hidden.
- **Single Postgres instance, one database per service.** The
  database-per-service boundary is enforced by separate databases and
  least-privilege roles, not by separate servers. In production these are
  separate instances/clusters with separate credentials and backups.
- `account`** \+ `ledger` ship as one deployment unit** (ADR-0001 D2) because of
  their accepted cycle. In production they could be split behind an async
  account-lifecycle projection, at the cost of eventual consistency on status
  checks.
- **Kafka is a single-node KRaft broker advertised as `localhost:9092`.** This is
  correct only while services run on the host, as they do in Stage 0. When
  services are containerized, the broker needs a dual listener (internal `
  kafka:9092` \+ external `localhost:9092`) or it hands in-network clients an
  unreachable address.
- `auth`** validation is local.** Each service fetches JWKS over HTTP and
  validates tokens itself, refreshing on a TTL (5–15 min); there is **no
  per-request auth RPC** (ADR-0001 D7). A revoked-but-unexpired access token
  stays valid for up to its TTL — accepted.
- **Antifraud's Kafka aggregation is deferred.** The synchronous rule engine and `
  CheckTransfer` ship with the ledger slice; only the daily-aggregate consumer
  waits for the antifraud stage (ADR-0001 D1).
- **External systems are mocks** (sanctions, OCR, SMS). No real integrations.
- **No Kubernetes, no multi-currency conversion, no payment rails, no frontend.**
  Docker Compose is the whole deployment story for this project.
