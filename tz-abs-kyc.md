# ТЗ: Pet-проект «Mini-Core» — упрощённая АБС + KYC на Go (микросервисы, gRPC)

**Уровень:** middle Go developer
**Срок:** 6–8 недель (part-time)
**Цель:** пройти полный цикл построения распределённой банковской системы: доменное моделирование, gRPC-контракты, асинхронные события, консистентность денег, наблюдаемость.

---

## 1. Что мы строим

Упрощённое ядро банка (АБС) с модулем KYC. Система обслуживает два сценария:

1. **Онбординг клиента:** регистрация → анкета → загрузка документов → KYC-проверка → одобрение → автоматическое открытие счёта.
2. **Денежный перевод:** клиент переводит деньги между счетами внутри банка с проверкой антифрода, двойной записью в леджере и уведомлением.

Всё внешнее (санкционные списки, SMS, распознавание документов) — моки. Смысл проекта — архитектура и корректность, а не интеграции.

## 2. Общие архитектурные требования

- Каждый сервис — отдельный Go-модуль (можно monorepo с `go.work`), гексагональная архитектура: `internal/domain`, `internal/app` (use cases), `internal/adapters` (grpc, postgres, kafka, redis).
- Синхронное взаимодействие между сервисами — **только gRPC**. Наружу торчит только API Gateway (REST через grpc-gateway или ручной HTTP-слой).
- Асинхронное взаимодействие — **Kafka** (события домена). Правило: команды — gRPC, факты — события.
- **Database per service.** Один PostgreSQL-инстанс, но у каждого сервиса своя БД/схема и свой пользователь. Ходить в чужую БД запрещено.
- Все proto-файлы — в отдельном каталоге `proto/` (или отдельном модуле `contracts`), генерация через `buf`. Версионирование пакетов: `bank.customer.v1` и т.д.
- Деньги — только `int64` в минорных единицах (тетри/копейки) + код валюты. `float` для денег = незачёт.
- Все ID — UUID v7. Все timestamp — UTC.
- Обязательные gRPC-интерцепторы в каждом сервисе: recovery, logging (slog), metrics (Prometheus), auth (проверка JWT из metadata), request-id propagation.
- Graceful shutdown, health checks (`grpc.health.v1`), readiness/liveness.
- Миграции — `goose` или `golang-migrate`, лежат рядом с сервисом, накатываются при старте или отдельной джобой.
- Идемпотентность всех операций, меняющих деньги (см. transaction-service).

## 3. Инфраструктура (docker-compose)

| Компонент | Назначение |
|---|---|
| PostgreSQL 16 | по БД на сервис |
| Kafka (KRaft, 1 брокер) | доменные события |
| Redis | идемпотентность, кеш, rate limit |
| MinIO | хранение документов KYC |
| Jaeger | трейсинг (OpenTelemetry) |
| Prometheus + Grafana | метрики |

Всё поднимается одной командой `make up`. В README — схема архитектуры (mermaid/excalidraw) и описание потоков.

## 4. Состав системы

| # | Сервис | Ответственность |
|---|---|---|
| 1 | api-gateway | вход снаружи, REST→gRPC, авторизация, rate limit |
| 2 | auth-service | учётки, JWT, RBAC |
| 3 | customer-service | профили клиентов |
| 4 | kyc-service | заявки на верификацию, статусная машина, проверки |
| 5 | document-service | файлы документов (MinIO) |
| 6 | account-service | счета, их жизненный цикл |
| 7 | ledger-service | проводки двойной записи, переводы |
| 8 | antifraud-service | правила и вердикты по операциям |
| 9 | notification-service | консьюмер событий, «отправка» уведомлений |

Роли пользователей: `CLIENT` (клиент банка), `OFFICER` (сотрудник KYC/операционист), `ADMIN`.

---

## 5. Сервисы и контракты

Ниже — ключевые методы. Сигнатуры даны в сокращённом виде; полные proto с полями сообщений проектируете сами — это часть задания. Общие требования к proto: у каждого запроса на изменение денег/статуса есть `idempotency_key`; листинги — с пагинацией (page_size/page_token); ошибки — через `google.rpc.Status` с осмысленными кодами (`NOT_FOUND`, `FAILED_PRECONDITION`, `ALREADY_EXISTS`, `INVALID_ARGUMENT`).

### 5.1 api-gateway

Единственная точка входа. REST (JSON) наружу, gRPC внутрь.

Задачи:
- Маппинг REST→gRPC (grpc-gateway или ручные хендлеры — на выбор, обосновать в README).
- Проверка JWT (валидация подписи локально, публичный ключ от auth-service), прокидывание claims в gRPC metadata.
- Rate limiting per-user через Redis (token bucket).
- Единый формат ошибок наружу: `{code, message, details, request_id}`.
- CORS, request-id, access-логи.

Своей БД нет. Бизнес-логики нет — если она появилась в gateway, это ошибка ревью.

### 5.2 auth-service

```proto
service AuthService {
  rpc Register(RegisterRequest) returns (RegisterResponse);            // email+пароль, роль CLIENT
  rpc Login(LoginRequest) returns (LoginResponse);                     // access + refresh
  rpc Refresh(RefreshRequest) returns (LoginResponse);
  rpc Logout(LogoutRequest) returns (google.protobuf.Empty);           // отзыв refresh
  rpc ValidateToken(ValidateTokenRequest) returns (ValidateTokenResponse); // для внутренних нужд
  rpc CreateOfficer(CreateOfficerRequest) returns (RegisterResponse);  // только ADMIN
}
```

Требования:
- Пароли — argon2id.
- Access JWT (RS256/EdDSA, TTL 15 мин) + refresh в Postgres с ротацией. Публичный JWKS-эндпоинт для gateway.
- В JWT: `sub` (user_id), `role`, `customer_id` (после привязки).
- При успешной регистрации клиента публикует событие `auth.user_registered` → его слушает customer-service и создаёт пустой профиль.

БД: `users`, `refresh_tokens`.

### 5.3 customer-service

```proto
service CustomerService {
  rpc GetCustomer(GetCustomerRequest) returns (Customer);
  rpc UpdateProfile(UpdateProfileRequest) returns (Customer);      // ФИО, дата рождения, адрес, телефон, гражданство
  rpc GetCustomerStatus(GetCustomerStatusRequest) returns (GetCustomerStatusResponse);
  rpc ListCustomers(ListCustomersRequest) returns (ListCustomersResponse); // OFFICER, фильтры по статусу
}
```

Статусы клиента: `NEW` → `PROFILE_FILLED` → `ON_KYC` → `ACTIVE` / `REJECTED` / `BLOCKED`.

Требования:
- Клиент видит/редактирует только себя (`customer_id` из JWT), OFFICER — всех. Проверка — в интерцепторе + use case.
- Редактировать профиль после `ON_KYC` нельзя (`FAILED_PRECONDITION`).
- Слушает `kyc.application_approved` / `kyc.application_rejected` и меняет статус.
- Публикует `customer.profile_filled` (по нему фронт/клиент может подавать KYC-заявку).

БД: `customers`.

### 5.4 kyc-service — центр проекта

```proto
service KycService {
  rpc SubmitApplication(SubmitApplicationRequest) returns (Application);   // клиент подаёт заявку
  rpc GetApplication(GetApplicationRequest) returns (Application);
  rpc ListApplications(ListApplicationsRequest) returns (ListApplicationsResponse); // очередь для OFFICER
  rpc ClaimApplication(ClaimApplicationRequest) returns (Application);     // OFFICER берёт в работу
  rpc ApproveApplication(ApproveApplicationRequest) returns (Application); // OFFICER
  rpc RejectApplication(RejectApplicationRequest) returns (Application);   // OFFICER, с причиной
  rpc RequestMoreDocuments(RequestMoreDocumentsRequest) returns (Application);
}
```

Статусная машина заявки (реализовать явно, отдельным типом с таблицей переходов + unit-тесты на все переходы):

```
DRAFT → SUBMITTED → AUTO_CHECKS → IN_REVIEW → APPROVED
                        │              ├→ REJECTED
                        └→ AUTO_REJECTED└→ DOCS_REQUESTED → SUBMITTED
```

Требования:
- При `SubmitApplication`: по gRPC проверяет в customer-service, что профиль заполнен, и в document-service, что загружены документы обязательных типов (паспорт + селфи).
- `AUTO_CHECKS` — асинхронный шаг (воркер): мок-проверка по «санкционному списку» (таблица ФИО+дата рождения, совпадение → `AUTO_REJECTED`), мок-проверка возраста ≥ 18. Результаты каждого чека сохраняются в `kyc_checks`.
- Одну заявку одновременно ревьюит один OFFICER (`ClaimApplication` — оптимистическая блокировка).
- Не более одной активной заявки на клиента (`ALREADY_EXISTS`).
- Полный аудит: таблица `kyc_events` — кто, когда, какой переход, комментарий.
- Публикует: `kyc.application_submitted`, `kyc.application_approved`, `kyc.application_rejected`, `kyc.docs_requested`.

БД: `applications`, `kyc_checks`, `kyc_events`, `sanctions_list`.

### 5.5 document-service

```proto
service DocumentService {
  rpc InitUpload(InitUploadRequest) returns (InitUploadResponse);      // тип документа → presigned PUT URL (MinIO)
  rpc ConfirmUpload(ConfirmUploadRequest) returns (Document);          // проверка, фиксация метаданных
  rpc GetDownloadUrl(GetDownloadUrlRequest) returns (GetDownloadUrlResponse); // presigned GET, TTL 5 мин
  rpc ListDocuments(ListDocumentsRequest) returns (ListDocumentsResponse);    // по customer_id
  rpc HasRequiredDocuments(HasRequiredDocumentsRequest) returns (HasRequiredDocumentsResponse); // для kyc-service
}
```

Требования:
- Типы: `PASSPORT`, `SELFIE`, `PROOF_OF_ADDRESS`. Лимит размера 10 МБ, только jpeg/png/pdf (проверка по magic bytes при Confirm).
- Файл ходит клиент↔MinIO напрямую по presigned URL, через gRPC файлы не гоняем.
- Доступ: клиент — только к своим документам, OFFICER — к документам клиентов из заявок.

БД: `documents` (метаданные, ключ в MinIO, статус UPLOADED/CONFIRMED, sha256).

### 5.6 account-service

```proto
service AccountService {
  rpc OpenAccount(OpenAccountRequest) returns (Account);        // валюта; клиент должен быть ACTIVE
  rpc GetAccount(GetAccountRequest) returns (Account);
  rpc ListAccounts(ListAccountsRequest) returns (ListAccountsResponse);
  rpc BlockAccount(BlockAccountRequest) returns (Account);      // OFFICER
  rpc CloseAccount(CloseAccountRequest) returns (Account);      // только при нулевом балансе
  rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse); // проксирует ledger-service
}
```

Требования:
- Статусы счёта: `ACTIVE` → `BLOCKED` → `ACTIVE`, `ACTIVE` → `CLOSED`.
- Номер счёта генерируется по маске (например, `GE` + чек-цифры + серийник) — детерминированно и уникально.
- Слушает `kyc.application_approved` → автоматически открывает первый счёт в GEL (вот где сходится онбординг).
- **Балансы не хранит.** Источник истины по деньгам — ledger-service; `GetBalance` ходит туда по gRPC. Это сознательное упрощение, зафиксировать в README.
- Лимит: не более 3 счетов на клиента в одной валюте.

БД: `accounts`.

### 5.7 ledger-service — второй центр проекта

```proto
service LedgerService {
  rpc Transfer(TransferRequest) returns (Transaction);           // from, to, amount, currency, idempotency_key
  rpc Deposit(DepositRequest) returns (Transaction);             // «пополнение из кассы» для тестов, OFFICER
  rpc GetTransaction(GetTransactionRequest) returns (Transaction);
  rpc ListTransactions(ListTransactionsRequest) returns (ListTransactionsResponse); // выписка по счёту
  rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse);
}
```

Модель — классическая двойная запись:
- `transactions` (id, status, type, created_at, idempotency_key UNIQUE);
- `entries` (id, transaction_id, account_id, direction DEBIT/CREDIT, amount, currency) — на каждую транзакцию минимум две записи, сумма дебетов = сумме кредитов (CHECK/инвариант, покрыть тестом);
- баланс счёта = агрегат по entries (для скорости — materialized `balances` с обновлением в той же транзакции БД).

Сценарий Transfer:
1. Идемпотентность: по `idempotency_key` вернуть существующую транзакцию, если уже была (UNIQUE + ON CONFLICT, не Redis-костыль).
2. gRPC в account-service: оба счёта существуют, ACTIVE, валюта совпадает.
3. gRPC в antifraud-service: вердикт. `DENY` → транзакция со статусом `DECLINED`, `FAILED_PRECONDITION` наружу.
4. В одной транзакции Postgres: проверка достаточности средств (`SELECT ... FOR UPDATE` по балансам в детерминированном порядке — устойчивость к дедлокам покрыть конкурентным тестом), запись entries, обновление балансов, запись в **outbox**.
5. Отдельный воркер публикует из outbox событие `ledger.transaction_completed` / `ledger.transaction_declined` в Kafka (at-least-once, событие с `event_id` для дедупликации у консьюмеров).

Обязательный тест: 100 конкурентных переводов между двумя счетами — итоговые балансы сходятся, отрицательный баланс невозможен, ни один перевод не потерян и не задвоен.

БД: `transactions`, `entries`, `balances`, `outbox`.

### 5.8 antifraud-service

```proto
service AntifraudService {
  rpc CheckTransfer(CheckTransferRequest) returns (CheckTransferResponse); // verdict: ALLOW | DENY, reasons[]
  rpc ListRules(ListRulesRequest) returns (ListRulesResponse);             // OFFICER
  rpc UpdateRule(UpdateRuleRequest) returns (Rule);                        // OFFICER, включение/пороги
}
```

Правила (каждое — отдельная реализация интерфейса `Rule`, конфигурируются из БД):
- разовый лимит суммы (например, > 10 000 GEL → DENY);
- velocity: не более N переводов за минуту с одного счёта (счётчики в Redis);
- дневной лимит суммы по счёту;
- перевод самому себе на тот же счёт → DENY.

Все проверки логируются в `af_checks` (кто, что, вердикт, сработавшие правила). Слушает `ledger.transaction_completed` для накопления дневных агрегатов.

### 5.9 notification-service

gRPC-API нет (или один метод `ListNotifications` для отладки). Чистый консьюмер Kafka:

- `kyc.application_approved` → «Поздравляем, вы верифицированы»;
- `kyc.application_rejected` / `kyc.docs_requested` → причина/запрос;
- `ledger.transaction_completed` → уведомления обеим сторонам.

Отправка — мок (запись в таблицу `notifications` + лог). Требования: дедупликация по `event_id`, retry с backoff, DLQ-топик для ядовитых сообщений.

---

## 6. Сквозные события Kafka (итоговый список)

| Топик | Событие | Producer | Consumers |
|---|---|---|---|
| auth.events | user_registered | auth | customer |
| customer.events | profile_filled | customer | — (фронт/логика) |
| kyc.events | application_submitted / approved / rejected / docs_requested | kyc | customer, account, notification |
| ledger.events | transaction_completed / declined | ledger | antifraud, notification |

Формат события: envelope `{event_id, event_type, occurred_at, payload}` в protobuf или JSON — выбрать и обосновать. Все продьюсеры денежных/статусных событий — только через outbox.

## 7. Наблюдаемость и качество

- OpenTelemetry-трейсинг сквозь всю цепочку: один trace от REST-запроса в gateway до записи в ledger и события в Kafka.
- Метрики: RED на каждый gRPC-метод + бизнес-метрики (заявки по статусам, объём переводов, вердикты антифрода).
- `golangci-lint` с общим конфигом, CI (GitHub Actions): lint → unit → интеграционные (testcontainers-go).
- Покрытие тестами: доменная логика (статусные машины, леджер, правила антифрода) — юниты; репозитории и gRPC — интеграционные через testcontainers; один e2e-скрипт «онбординг + перевод» поверх docker-compose.

## 8. Этапы сдачи

1. **Неделя 1:** каркас monorepo, buf, proto v1 всех сервисов, docker-compose инфраструктуры, шаблон сервиса с интерцепторами и health checks.
2. **Недели 2–3:** auth + customer + gateway. Работает регистрация, логин, заполнение профиля через REST.
3. **Недели 3–4:** document + kyc. Полный онбординг до APPROVED, включая авто-чеки и ручное ревью.
4. **Недели 5–6:** account + ledger. Автооткрытие счёта по событию, Deposit, Transfer с идемпотентностью и конкурентным тестом.
5. **Неделя 7:** antifraud + notification, outbox везде, DLQ.
6. **Неделя 8:** наблюдаемость, e2e, README с диаграммами, демо.

Каждый этап — отдельный PR (или серия PR) с ревью. Без ревью этап не считается сданным.

## 9. Definition of Done (на проект)

- `make up && make e2e` на чистой машине проходит полный сценарий: регистрация → профиль → документы → KYC approve офицером → счёт открыт → deposit → transfer → уведомления записаны.
- Конкурентный тест леджера зелёный; повторный Transfer с тем же idempotency_key возвращает ту же транзакцию.
- Ни один сервис не читает чужую БД; бизнес-логики в gateway нет; деньги нигде не float.
- README: схема архитектуры, описание потоков, список осознанных упрощений и что бы вы сделали иначе в проде.

## 10. Явно вне скоупа

Мультивалютная конвертация, внешние платёжные рельсы (SWIFT/SEPA), реальные санкционные API и OCR документов, карты и процессинг, шардирование, Kubernetes (по желанию — бонус), фронтенд (достаточно Postman/грамотных curl-скриптов).

## 11. Бонус-задачи (для сильных)

- Saga с компенсацией: блокировка счёта во время «расследования» антифрода с заморозкой/разморозкой средств.
- gRPC streaming: `WatchApplication` — офицерская очередь KYC в реальном времени.
- Chaos-тест: убить ledger-service посреди нагрузки и показать, что деньги сошлись после рестарта.
