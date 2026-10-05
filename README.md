# Currency Exchange Rate Service (Plata — Go Engineer Test Assignment)

[![CI](https://github.com/ArtemPak289/Currency-exchange-rate-service/actions/workflows/ci.yml/badge.svg)](https://github.com/ArtemPak289/Currency-exchange-rate-service/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-blue.svg)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-336791.svg)](https://www.postgresql.org)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com)
[![Swagger](https://img.shields.io/badge/Swagger-OpenAPI%203.0-85EA2D.svg)](http://localhost:8080/swagger/)

A production-grade, asynchronous Foreign Exchange (FX) rate service written in Go. The service provides a non-blocking asynchronous HTTP JSON API for requesting exchange rate refreshes, querying quote statuses by request ID, and retrieving the latest recorded rates.

All extra assignment tasks are implemented:
- **Supported Currencies**: Full cross-rate support for **USD, EUR, MXN**, and **Uzbek Som (UZS)** with intelligent colloquial alias normalization (`SUM`, `SUMM`, `SOM`).
- **Unit & Integration Tests** (`go test -race ./...`) with full coverage of domain, service, worker pool, and HTTP layers.
- **Containerization** via multi-stage hardened Dockerfile and Docker Compose with healthchecks.
- **Idempotency** supporting both explicit `Idempotency-Key` headers and automatic in-flight deduplication.
- **Interactive Swagger / OpenAPI 3.0 UI** embedded directly into the binary at `http://localhost:8080/swagger/`.
- **Database Migrations** applied automatically on boot using embedded SQL scripts.

---

## Architecture & System Design

The project follows **Clean Architecture / Hexagonal Architecture** principles, strictly decoupling domain logic from transport protocols (HTTP) and persistent storage (PostgreSQL).

```mermaid
flowchart TD
    Client[HTTP Client / Browser]
    
    subgraph HTTP Layer [Transport Layer - Chi Router]
        H_Post["POST /api/v1/quotes (Refresh)"]
        H_GetID["GET /api/v1/quotes/{id}"]
        H_GetLatest["GET /api/v1/quotes/latest"]
        H_Swagger["GET /swagger/ (Swagger UI)"]
        H_Health["GET /health & /ready"]
    end
    
    subgraph Core [Service & Business Logic]
        QuoteService["QuoteService (Idempotency & Dedup)"]
    end

    subgraph AsyncWorker [Asynchronous Worker Engine]
        JobQueue[Buffered Job Channel]
        WorkerPool[Worker Goroutines Pool]
    end

    subgraph External [External FX Provider]
        ExchangeClient["ExchangeRatesAPI Client (Cross-rate engine + TTL cache)"]
        ExternalAPI["api.exchangeratesapi.io"]
    end

    subgraph Storage [Database Layer]
        Repo["QuoteRepository (PostgreSQL)"]
        T_Requests[("quote_requests table")]
        T_Latest[("latest_quotes table")]
    end

    Client -->|Refresh request| H_Post
    Client -->|Status query| H_GetID
    Client -->|Latest rate query| H_GetLatest
    Client -->|Interactive docs| H_Swagger

    H_Post --> QuoteService
    H_GetID --> QuoteService
    H_GetLatest --> QuoteService

    QuoteService -->|Insert PENDING & Check Idempotency| Repo
    QuoteService -->|Enqueue task| JobQueue
    
    JobQueue --> WorkerPool
    WorkerPool -->|Fetch rate| ExchangeClient
    ExchangeClient --> ExternalAPI
    WorkerPool -->|Save COMPLETED & Latest Quote Tx| Repo
    
    Repo --> T_Requests
    Repo --> T_Latest
```

### Key Engineering Decisions

1. **Non-blocking Asynchronous Refresh**:
   - When a client sends `POST /api/v1/quotes`, the handler creates a `PENDING` database record, enqueues the job to an internal channel, and immediately responds with `HTTP 202 Accepted` and the update UUID.
   - Dedicated worker goroutines process updates in the background, updating status to `PROCESSING` and then `COMPLETED` (or `FAILED` with detailed reason).

2. **Cross-Rate Calculation Engine**:
   - The free tier of `exchangeratesapi.io` restricts base currency to `EUR`.
   - The exchange client fetches EUR-based rates and dynamically computes cross-rates for arbitrary currency pairs:
     $$\text{Rate}(\text{Base} \to \text{Quote}) = \frac{\text{Rate}(\text{EUR} \to \text{Quote})}{\text{Rate}(\text{EUR} \to \text{Base})}$$
   - Example: For `USD/MXN`, $\text{Rate} = \frac{\text{Rate}(\text{EUR} \to \text{MXN})}{\text{Rate}(\text{EUR} \to \text{USD})}$.

3. **In-Memory Rate Caching with TTL**:
   - To respect external API rate limits and conserve quota, the exchange client caches upstream rates with a configurable TTL (`EXCHANGE_API_CACHE_TTL`, default 30s). Concurrent or rapid requests reuse cached rates without hitting external servers.

4. **Idempotency & Deduplication**:
   - **Explicit Idempotency Key**: Clients can supply an `Idempotency-Key` HTTP header. Repeated requests return the original request details (`HTTP 200` or `202`) with header `Idempotent-Replayed: true`. Reusing a key with a conflicting currency payload returns `HTTP 409 Conflict`.
   - **In-flight Deduplication**: If no key is provided, the service detects if an update for that currency is already `PENDING` or `PROCESSING` within the deduplication window (default 15s) and avoids spawning duplicate external queries.

5. **Startup Recovery**:
   - When the service restarts or recovers from a crash, the worker pool queries PostgreSQL for any orphaned `PENDING` or `PROCESSING` requests and automatically re-enqueues them.

6. **Embedded Database Migrations**:
   - SQL migrations are embedded into the Go binary with `embed.FS` and executed automatically on startup. No external CLI tools are needed.

---

## Quick Start with Docker (Recommended)

Start the application and PostgreSQL database with a single command:

```bash
docker compose up --build -d
```

Check service status and logs:

```bash
docker compose ps
docker compose logs -f app
```

Test that the service is running:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Access the **interactive Swagger UI** in your browser:
👉 [http://localhost:8080/swagger/](http://localhost:8080/swagger/)

To stop the services:

```bash
docker compose down -v
```

---

## Running Locally without Docker

### Prerequisites
- Go 1.24+ installed
- PostgreSQL 14+ running locally (or running in-memory mode automatically if no DB is provided)

### Steps

1. Copy `.env.example` to `.env`:
   ```bash
   cp .env.example .env
   ```

2. Run the application:
   ```bash
   go run ./cmd/server
   ```
   *(Note: If PostgreSQL is not detected, the service automatically falls back to an in-memory repository for zero-friction local development!)*

---

## API Reference

### 1. Update Quote (Asynchronous)

Schedules a background update for a currency pair.

- **URL**: `POST /api/v1/quotes` (or `/api/v1/quotes/refresh`)
- **Headers**:
  - `Content-Type: application/json`
  - `Idempotency-Key: <unique-uuid>` *(optional, guarantees idempotent replay)*
- **Body**:
  ```json
  {
    "currency": "EUR/MXN"
  }
  ```
- **Responses**:
  - `202 Accepted`: Update scheduled successfully.
    ```json
    {
      "id": "b69a6a5e-a072-4ae1-8687-001316693b4e",
      "currency": "EUR/MXN",
      "status": "PENDING",
      "created_at": "2026-10-04T01:14:28.453837Z"
    }
    ```
  - `200 OK`: Idempotent replay of an already processed request (`Idempotent-Replayed: true`).
  - `400 Bad Request`: Invalid currency pair format (e.g. `INVALID`, `EUR/EUR`).
  - `409 Conflict`: Idempotency key conflict with a different currency payload.

#### Example `curl`:
```bash
# Refresh EUR/MXN
curl -i -X POST http://localhost:8080/api/v1/quotes \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: tx-demo-1001" \
  -d '{"currency": "EUR/MXN"}'

# Refresh USD/SUMM (Uzbek Som)
curl -i -X POST http://localhost:8080/api/v1/quotes \
  -H "Content-Type: application/json" \
  -d '{"currency": "USD/SUMM"}'
```

---

### 2. Get Quote by ID

Retrieves current status, price, and update time for a previous update request.

- **URL**: `GET /api/v1/quotes/{id}`
- **Responses**:
  - `200 OK`:
    ```json
    {
      "id": "b69a6a5e-a072-4ae1-8687-001316693b4e",
      "currency": "EUR/MXN",
      "status": "COMPLETED",
      "price": 20.447892,
      "created_at": "2026-10-04T01:14:28.453837Z",
      "updated_at": "2026-10-04T01:14:29.252398Z"
    }
    ```
  - `404 Not Found`: Request ID does not exist.
  - `400 Bad Request`: Malformed UUID.

#### Example `curl`:
```bash
curl -s http://localhost:8080/api/v1/quotes/b69a6a5e-a072-4ae1-8687-001316693b4e
```

---

### 3. Get Latest Quote

Returns the most recently recorded exchange rate for a currency pair.

- **URL**: `GET /api/v1/quotes/latest?currency=EUR/MXN` *(also supports path syntax: `/api/v1/quotes/latest/EUR/MXN`)*
- **Responses**:
  - `200 OK`:
    ```json
    {
      "currency": "EUR/MXN",
      "price": 20.447892,
      "updated_at": "2026-10-04T01:14:29.252398Z"
    }
    ```
  - `404 Not Found`: No quote has been recorded yet for this currency.
  - `400 Bad Request`: Missing or invalid currency parameter.

#### Example `curl`:
```bash
curl -s "http://localhost:8080/api/v1/quotes/latest?currency=EUR/MXN"
```

---

### 4. Health & System Probes

- `GET /health`: Liveness probe (returns HTTP 200 `{"status":"healthy"}`).
- `GET /ready`: Readiness probe (verifies PostgreSQL database connection).
- `GET /swagger/`: Interactive Swagger UI.
- `GET /swagger/openapi.yaml`: Raw OpenAPI 3.0 specification.
- `GET /swagger/swagger.json`: Raw OpenAPI specification in JSON format.

---

## Configuration

Environment variables can be configured in `.env` or passed through the container environment:

| Variable | Default Value | Description |
|---|---|---|
| `HTTP_PORT` | `8080` | Port for the HTTP server |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable` | PostgreSQL connection string |
| `EXCHANGE_API_KEY` | `2270434cad5887ab4ccc50fd5d8cfd16` | API key for exchangeratesapi.io |
| `EXCHANGE_API_BASE_URL`| `http://api.exchangeratesapi.io/v1/latest` | Base endpoint of exchange rates provider |
| `EXCHANGE_API_TIMEOUT` | `10s` | HTTP timeout when calling external provider |
| `EXCHANGE_API_CACHE_TTL` | `30s` | Cache duration for upstream rates |
| `USE_MOCK_FETCHER` | `false` | Enable deterministic mock fetcher for testing |
| `WORKER_COUNT` | `3` | Number of concurrent background worker goroutines |
| `WORKER_QUEUE_SIZE` | `1000` | In-memory job queue buffer size |
| `WORKER_MAX_RETRIES` | `3` | Retry attempts before marking request FAILED |
| `WORKER_RETRY_DELAY` | `1s` | Initial delay for exponential backoff retry |
| `DEDUPLICATION_WINDOW`| `15s` | Window for deduplicating in-flight requests |
| `ENVIRONMENT` | `development` | Environment mode (`development` or `production`) |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |

---

## Running Tests

Run all unit, integration, and E2E test suites with race detection:

```bash
make test-race
```

Run test suite with code coverage:

```bash
make test-cover
```

Test summary:
- **`internal/domain`**: Currency pair normalization, ISO validation, formatting.
- **`internal/exchange`**: Mock fetcher, HTTP client, caching, thread safety, cross-rate math.
- **`internal/repository`**: In-memory and PostgreSQL storage, queries, transitions.
- **`internal/worker`**: Goroutine pool, retry handling with backoff, graceful draining.
- **`internal/service`**: Idempotency key handling, conflict detection, in-flight deduplication.
- **`internal/handler/http`**: Controller unit tests, status codes (200, 202, 400, 404, 409).
- **`test/e2e_test.go`**: Full end-to-end integration lifecycle testing.

---

## Makefile Commands

| Command | Action |
|---|---|
| `make build` | Compiles binary to `bin/server` |
| `make run` | Builds and runs server locally |
| `make test` | Runs all unit and integration tests |
| `make test-race` | Runs tests with Go `-race` detector |
| `make test-cover` | Runs tests and prints coverage stats |
| `make lint` | Runs `go vet ./...` |
| `make docker-build` | Builds Docker image |
| `make docker-up` | Starts PostgreSQL and app in background via Docker Compose |
| `make docker-down`| Stops Docker containers and cleans volumes |

---

## Project Structure

```
.
├── cmd/
│   └── server/
│       └── main.go               # Main application entrypoint
├── internal/
│   ├── config/                   # Configuration management
│   │   ├── config.go
│   │   └── config_test.go
│   ├── domain/                   # Domain entities and error types
│   │   ├── quote.go
│   │   └── quote_test.go
│   ├── exchange/                 # FX rates client (API & mock)
│   │   ├── client.go
│   │   ├── exchangeratesapi.go
│   │   ├── mock.go
│   │   └── client_test.go
│   ├── handler/
│   │   └── http/                 # HTTP Chi router & handlers
│   │       ├── handler.go
│   │       ├── quote_handler.go
│   │       ├── quote_handler_test.go
│   │       ├── middleware.go
│   │       └── response.go
│   ├── repository/               # Data access contracts & implementations
│   │   ├── repository.go
│   │   ├── memory/               # In-memory implementation
│   │   │   ├── memory_repository.go
│   │   │   └── memory_repository_test.go
│   │   └── postgres/             # PostgreSQL implementation & migrations
│   │       ├── quote_repository.go
│   │       ├── migrations.go
│   │       └── migrations/
│   │           ├── 000001_init.up.sql
│   │           └── 000001_init.down.sql
│   ├── service/                  # Business logic (idempotency, dedup)
│   │   ├── quote_service.go
│   │   └── quote_service_test.go
│   └── worker/                   # Background worker pool & retry engine
│       ├── pool.go
│       └── pool_test.go
├── api/
│   ├── api.go                    # Go embed wrapper for OpenAPI & Swagger UI
│   ├── openapi.yaml              # OpenAPI 3.0 specification
│   ├── swagger.json              # OpenAPI JSON specification
│   └── swagger-ui/
│       └── index.html            # Embedded Swagger UI
├── test/
│   └── e2e_test.go               # End-to-end integration test
├── .github/
│   └── workflows/
│       └── ci.yml                # Automated CI pipeline
├── Dockerfile                    # Multi-stage production container
├── docker-compose.yml            # PostgreSQL + App orchestration
├── .dockerignore
├── .env.example
├── Makefile
├── README.md                     # Documentation in English
└── READMERU.md                   # Documentation in Russian
```

---

## Author
**Artem Pak**  
GitHub: [@ArtemPak289](https://github.com/ArtemPak289)