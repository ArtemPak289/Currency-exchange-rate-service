# Сервис курсов обмена валют (Currency Exchange Rate Service — Plata Test Assignment)

[![CI](https://github.com/ArtemPak289/Currency-exchange-rate-service/actions/workflows/ci.yml/badge.svg)](https://github.com/ArtemPak289/Currency-exchange-rate-service/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-blue.svg)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-336791.svg)](https://www.postgresql.org)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com)
[![Swagger](https://img.shields.io/badge/Swagger-OpenAPI%203.0-85EA2D.svg)](http://localhost:8080/swagger/)

[🇬🇧 Read documentation in English (README.md)](README.md)

Production-grade асинхронный микросервис для получения и отслеживания курсов валют (FX), написанный на Go. Сервис предоставляет неблокирующий асинхронный HTTP JSON API для запроса обновлений котировок, проверки статуса задач по их UUID и получения самых актуальных сохраненных курсов.

### Реализованные функциональные требования:
- **Поддерживаемые валюты**: Полная поддержка кросс-курсов для **USD, EUR, MXN** и **Узбекского сума (UZS)** с нормализацией разговорных названий и синонимов (`SUM`, `SUMM`, `SOM`, `SO'M`).
- **Тестирование**: Модульные, интеграционные и E2E-тесты (`go test -race ./...`) с полным покрытием слоев domain, service, worker pool и HTTP-контроллеров.
- **Контейнеризация**: Многоэтапный (multi-stage) минимальный Dockerfile и конфигурация Docker Compose со встроенными healthcheck-проверками.
- **Идемпотентность**: Поддержка заголовка `Idempotency-Key` с отслеживанием конфликтов полезной нагрузки и автоматическая in-flight дедупликация параллельных запросов.
- **Интерактивный Swagger / OpenAPI 3.0 UI**: Встроен прямо в бинарный файл по адресу `http://localhost:8080/swagger/`.
- **Автоматические миграции БД**: Выполняются при старте приложения с помощью встроенных SQL-скриптов (`embed.FS`).
- **Zero-Dependency Fallback**: При отсутствии подключения к PostgreSQL сервис автоматически переключается на потокобезопасный in-memory репозиторий для локальной разработки.

---

## Архитектура и системный дизайн

Проект построен по принципам **Чистой / Гексагональной архитектуры (Clean / Hexagonal Architecture)**, изолируя бизнес-логику домена от внешних протоколов передачи данных (HTTP) и хранилища (PostgreSQL).

```mermaid
flowchart TD
    Client[HTTP-клиент / Браузер]
    
    subgraph HTTP Layer [Транспортный слой - Chi Router]
        H_Post["POST /api/v1/quotes (Обновление)"]
        H_GetID["GET /api/v1/quotes/{id}"]
        H_GetLatest["GET /api/v1/quotes/latest"]
        H_Swagger["GET /swagger/ (Swagger UI)"]
        H_Health["GET /health & /ready"]
    end
    
    subgraph Core [Слой бизнес-логики]
        QuoteService["QuoteService (Идемпотентность и дедупликация)"]
    end

    subgraph AsyncWorker [Асинхронный пул воркеров]
        JobQueue[Буферизированный канал задач]
        WorkerPool[Пул воркер-горутин]
    end

    subgraph External [Внешний провайдер курсов]
        ExchangeClient["ExchangeRatesAPI Client (Кросс-курс расчет + TTL кэш)"]
        ExternalAPI["api.exchangeratesapi.io"]
    end

    subgraph Storage [Слой хранения данных]
        Repo["QuoteRepository (PostgreSQL / In-Memory)"]
        T_Requests[("quote_requests table")]
        T_Latest[("latest_quotes table")]
    end

    Client -->|Запрос обновления| H_Post
    Client -->|Проверка статуса| H_GetID
    Client -->|Запрос последнего курса| H_GetLatest
    Client -->|Интерактивная документация| H_Swagger

    H_Post --> QuoteService
    H_GetID --> QuoteService
    H_GetLatest --> QuoteService

    QuoteService -->|Создать запись PENDING / Проверить ключ| Repo
    QuoteService -->|Поставить задачу в очередь| JobQueue
    
    JobQueue --> WorkerPool
    WorkerPool -->|Запросить курс| ExchangeClient
    ExchangeClient --> ExternalAPI
    WorkerPool -->|Транзакция: статус COMPLETED + Latest Quote| Repo
    
    Repo --> T_Requests
    Repo --> T_Latest
```

### Ключевые инженерные решения

1. **Неблокирующая асинхронная обработка**:
   - При вызове `POST /api/v1/quotes` хэндлер создает запись со статусом `PENDING`, отправляет задачу во внутренний буферизированный канал и **мгновенно возвращает ответ `HTTP 202 Accepted`** с UUID задачи.
   - Пул воркеров забирает задачи из очереди, переводит их в `PROCESSING`, делает запрос к внешнему API и переводит в `COMPLETED` (либо `FAILED` с сохранением причины ошибки).

2. **Движок расчета кросс-курсов (Cross-Rate Engine)**:
   - Бесплатный тариф `exchangeratesapi.io` жестко фиксирует базовую валюту как `EUR`.
   - Клиент сервиса запрашивает котировки относительно EUR и динамически рассчитывает любые кросс-курсы по формуле:
     $$\text{Rate}(\text{Base} \to \text{Quote}) = \frac{\text{Rate}(\text{EUR} \to \text{Quote})}{\text{Rate}(\text{EUR} \to \text{Base})}$$
   - *Пример:* для пары `USD/MXN`: $\text{Rate} = \frac{\text{Rate}(\text{EUR} \to \text{MXN})}{\text{Rate}(\text{EUR} \to \text{USD})}$.

3. **In-Memory кэширование с TTL**:
   - Для соблюдения лимитов внешнего API полученные курсы кэшируются в памяти с настраиваемым временем жизни (`EXCHANGE_API_CACHE_TTL`, по умолчанию 30 сек). Повторные и параллельные запросы используют кэш.

4. **Идемпотентность и дедупликация**:
   - **Явный ключ идемпотентности**: Клиент может передать заголовок `Idempotency-Key`. При повторении запроса возвращается исходная задача с заголовком `Idempotent-Replayed: true`. При попытке использовать тот же ключ для другой валюты возвращается `HTTP 409 Conflict`.
   - **In-flight дедупликация**: Если ключ не указан, сервис проверяет, нет ли уже выполняющейся задачи (`PENDING`/`PROCESSING`) для данной валюты в рамках временного окна (`DEDUPLICATION_WINDOW`, по умолчанию 15 сек), предотвращая дублирование внешних сетевых вызовов.

5. **Восстановление после падений (Startup Recovery)**:
   - При старте или перезапуске приложения пул воркеров сканирует БД на наличие незавершенных задач (`PENDING` или `PROCESSING`) и автоматически повторно ставит их в очередь на обработку.

6. **Встроенные миграции БД**:
   - SQL-миграции вкомпилированы в бинарник через `embed.FS` и применяются автоматически при старте сервиса. Сторонние утилиты не требуются.

---

## Быстрый старт через Docker (Рекомендуется)

Запуск приложения и базы данных PostgreSQL одной командой:

```bash
docker compose up --build -d
```

Проверка состояния и логов:

```bash
docker compose ps
docker compose logs -f app
```

Проверка работоспособности сервиса:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Интерактивная документация **Swagger UI** доступна в браузере:
👉 [http://localhost:8080/swagger/](http://localhost:8080/swagger/)

Остановка контейнеров:

```bash
docker compose down -v
```

---

## Локальный запуск без Docker

### Требования
- Установленный Go 1.24+
- Локальный PostgreSQL 14+ (или без него — сервис автоматически перейдет на in-memory режим!)

### Шаги

1. Скопируйте файл конфигурации:
   ```bash
   cp .env.example .env
   ```

2. Запустите сервис:
   ```bash
   go run ./cmd/server
   ```
   *(Если база данных PostgreSQL не обнаружена, сервис автоматически переключится на встроенный in-memory репозиторий).*

---

## Справочник API

### 1. Запрос на обновление котировки (Асинхронный)

Создает фоновую задачу на получение актуального курса для указанной валютной пары.

- **URL**: `POST /api/v1/quotes` *(или `/api/v1/quotes/refresh`)*
- **Заголовки**:
  - `Content-Type: application/json`
  - `Idempotency-Key: <уникальный-ключ>` *(опционально)*
- **Тело запроса**:
  ```json
  {
    "currency": "EUR/MXN"
  }
  ```
- **Ответы**:
  - `202 Accepted`: Задача успешно поставлена в очередь.
    ```json
    {
      "id": "b69a6a5e-a072-4ae1-8687-001316693b4e",
      "currency": "EUR/MXN",
      "status": "PENDING",
      "created_at": "2026-10-05T11:00:00Z"
    }
    ```
  - `200 OK`: Идемпотентный повтор уже обработанного запроса (`Idempotent-Replayed: true`).
  - `400 Bad Request`: Некорректный формат валюты (например, `EUR/EUR`, `INVALID`).
  - `409 Conflict`: Ключ идемпотентности уже использован для другой валюты.

#### Примеры `curl`:
```bash
# Обновление курса EUR/MXN с ключом идемпотентности
curl -i -X POST http://localhost:8080/api/v1/quotes \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: tx-demo-1001" \
  -d '{"currency": "EUR/MXN"}'

# Обновление курса USD/SUMM (Узбекский сум автоматически преобразуется в UZS)
curl -i -X POST http://localhost:8080/api/v1/quotes \
  -H "Content-Type: application/json" \
  -d '{"currency": "USD/SUMM"}'
```

---

### 2. Получение статуса котировки по ID

Возвращает текущий статус, полученную цену и время обновления для указанного ID задачи.

- **URL**: `GET /api/v1/quotes/{id}`
- **Ответы**:
  - `200 OK`:
    ```json
    {
      "id": "b69a6a5e-a072-4ae1-8687-001316693b4e",
      "currency": "EUR/MXN",
      "status": "COMPLETED",
      "price": 20.447892,
      "created_at": "2026-10-05T11:00:00Z",
      "updated_at": "2026-10-05T11:00:01Z"
    }
    ```
  - `404 Not Found`: Задача с таким UUID не найдена.
  - `400 Bad Request`: Некорректный формат UUID.

#### Пример `curl`:
```bash
curl -s http://localhost:8080/api/v1/quotes/b69a6a5e-a072-4ae1-8687-001316693b4e
```

---

### 3. Получение последней котировки валюты

Возвращает самый свежий зафиксированный курс для валютной пары.

- **URL**: `GET /api/v1/quotes/latest?currency=EUR/MXN` *(или `/api/v1/quotes/latest/EUR/MXN`)*
- **Ответы**:
  - `200 OK`:
    ```json
    {
      "currency": "EUR/MXN",
      "price": 20.447892,
      "updated_at": "2026-10-05T11:00:01Z"
    }
    ```
  - `404 Not Found`: Для этой валютной пары еще нет сохраненных котировок.
  - `400 Bad Request`: Не передан параметр валюты или неверный формат.

#### Пример `curl`:
```bash
curl -s "http://localhost:8080/api/v1/quotes/latest?currency=EUR/MXN"
```

---

### 4. Системные и диагностические эндпоинты

- `GET /health`: Liveness-проба (`{"status":"healthy"}`).
- `GET /ready`: Readiness-проба (проверяет доступность PostgreSQL).
- `GET /swagger/`: Интерактивный интерфейс Swagger UI.
- `GET /swagger/openapi.yaml`: Спецификация OpenAPI 3.0 в формате YAML.
- `GET /swagger/swagger.json`: Спецификация OpenAPI в формате JSON.

---

## Конфигурация

Все параметры конфигурируются через переменные окружения в `.env` или через Docker Compose:

| Переменная | Значение по умолчанию | Описание |
|---|---|---|
| `HTTP_PORT` | `8080` | Порт HTTP-сервера |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable` | Строка подключения к PostgreSQL |
| `EXCHANGE_API_KEY` | `2270434cad5887ab4ccc50fd5d8cfd16` | API-ключ для exchangeratesapi.io |
| `EXCHANGE_API_BASE_URL`| `http://api.exchangeratesapi.io/v1/latest` | URL API провайдера курсов валют |
| `EXCHANGE_API_TIMEOUT` | `10s` | Таймаут HTTP-запросов к внешнему провайдеру |
| `EXCHANGE_API_CACHE_TTL` | `30s` | Время жизни кэша курсов валют в памяти |
| `USE_MOCK_FETCHER` | `false` | Использовать мок-провайдер для автономных тестов |
| `WORKER_COUNT` | `3` | Количество параллельных воркеров (горутин) |
| `WORKER_QUEUE_SIZE` | `1000` | Размер буфера очереди задач в памяти |
| `WORKER_MAX_RETRIES` | `3` | Количество повторных попыток при сбое |
| `WORKER_RETRY_DELAY` | `1s` | Начальная задержка при экспоненциальном backoff |
| `DEDUPLICATION_WINDOW`| `15s` | Окно дедупликации in-flight запросов без ключа |
| `ENVIRONMENT` | `development` | Режим работы (`development` или `production`) |
| `LOG_LEVEL` | `info` | Уровень логирования (`debug`, `info`, `warn`, `error`) |

---

## Запуск тестов

Запуск модульных, интеграционных и E2E тестов с детектором гонок (Race Detector):

```bash
make test-race
```

Запуск тестов с подсчетом покрытия кода:

```bash
make test-cover
```

Обзор тестового покрытия:
- **`internal/domain`**: Нормализация синонимов (`SUM`, `SOM` -> `UZS`), валидация ISO-кодов, форматирование.
- **`internal/exchange`**: HTTP-клиент, мок-клиент, расчет кросс-курсов, потокобезопасность кэша с TTL.
- **`internal/repository`**: Тесты PostgreSQL и in-memory хранилища, поиск in-flight задач, уникальность ключей.
- **`internal/worker`**: Пул горутин, обработка очередей, повторы с задержкой, корректная остановка (drain).
- **`internal/service`**: Проверка идемпотентности, обнаружение конфликтов, окно дедупликации.
- **`internal/handler/http`**: HTTP-контроллеры, заголовки `Idempotent-Replayed`, коды 200, 202, 400, 404, 409.
- **`test/e2e_test.go`**: Полный сквозной цикл выполнения запроса через реальный HTTP-сервер.

---

## Команды Makefile

| Команда | Описание |
|---|---|
| `make build` | Компилирует исполняемый файл в `bin/server` |
| `make run` | Собирает и запускает сервер локально |
| `make test` | Запускает все тесты |
| `make test-race` | Запускает тесты с флагом проверки гонок (`-race`) |
| `make test-cover` | Запускает тесты и выводит процент покрытия кода |
| `make lint` | Проверяет код с помощью `go vet ./...` |
| `make docker-build` | Собирает Docker-образ |
| `make docker-up` | Запускает PostgreSQL и приложение в фоне через Docker Compose |
| `make docker-down`| Останавливает контейнеры и очищает volume |

---

## Структура проекта

```
.
├── cmd/
│   └── server/
│       └── main.go               # Точка входа, DI, graceful shutdown
├── internal/
│   ├── config/                   # Загрузка и валидация конфигурации
│   │   ├── config.go
│   │   └── config_test.go
│   ├── domain/                   # Доменные модели, сущности и ошибки
│   │   ├── quote.go
│   │   └── quote_test.go
│   ├── exchange/                 # Клиент внешнего FX API и движок кросс-курсов
│   │   ├── client.go
│   │   ├── exchangeratesapi.go
│   │   ├── mock.go
│   │   └── client_test.go
│   ├── handler/
│   │   └── http/                 # Роутер Chi, HTTP-хэндлеры и middleware
│   │       ├── handler.go
│   │       ├── quote_handler.go
│   │       ├── quote_handler_test.go
│   │       ├── middleware.go
│   │       └── response.go
│   ├── repository/               # Интерфейсы и реализации хранилища
│   │   ├── repository.go
│   │   ├── memory/               # In-memory репозиторий (fallback)
│   │   │   ├── memory_repository.go
│   │   │   └── memory_repository_test.go
│   │   └── postgres/             # Реализация PostgreSQL и миграции
│   │       ├── quote_repository.go
│   │       ├── migrations.go
│   │       └── migrations/
│   │           ├── 000001_init.up.sql
│   │           └── 000001_init.down.sql
│   ├── service/                  # Бизнес-логика (идемпотентность, дедупликация)
│   │   ├── quote_service.go
│   │   └── quote_service_test.go
│   └── worker/                   # Пул фоновых воркеров и механизм повторов
│       ├── pool.go
│       └── pool_test.go
├── api/
│   ├── api.go                    # Встраивание OpenAPI и Swagger UI через go:embed
│   ├── openapi.yaml              # Спецификация OpenAPI 3.0 (YAML)
│   ├── swagger.json              # Спецификация OpenAPI 3.0 (JSON)
│   └── swagger-ui/
│       └── index.html            # Встроенная страница Swagger UI
├── test/
│   └── e2e_test.go               # Сквозные интеграционные E2E-тесты
├── .github/
│   └── workflows/
│       └── ci.yml                # CI пайплайн GitHub Actions
├── Dockerfile                    # Многоэтапный Dockerfile
├── docker-compose.yml            # Запуск PostgreSQL и приложения
├── .dockerignore
├── .env.example
├── Makefile
├── README.md                     # Документация на английском языке
└── READMERU.md                   # Документация на русском языке
```

---

## Автор
**Artem Pak**  
GitHub: [@ArtemPak289](https://github.com/ArtemPak289)
