# PR Reviewer Assignment Service

Микросервис для автоматического назначения ревьюеров на PR.

## Возможности
- Автоназначение до двух активных ревьюеров из команды автора PR (автор не назначается).
- Переназначение ревьюера на случайного активного участника его команды; запрет после `MERGED`.
- Идемпотентный merge PR.
- Массовая деактивация команды с безопасным пересчётом ревьюеров открытых PR.
- Получение PR, назначенных пользователю, и агрегированной статистики.
- Health-check и нагрузочный прогон (см. [docs/loadtest.md](docs/loadtest.md)).

## Технологии и структура
- Go (stdlib `net/http`), PostgreSQL (pgx), zap-логирование, golangci-lint.
- Миграции: `internal/storage/migrations` применяются на старте.
- Слои: `internal/api` (handlers), `internal/service` (бизнес-логика), `internal/storage` (Postgres + транзакции с ретраями), `configs` (env).
- Дополнительно: интеграционные тесты на testcontainers, нагрузочное тестирование (`tools/loadtest.go`).

## Запуск
- Docker Compose из корня: `docker compose up --build` (поднимет Postgres, применит миграции, сервис на `:8080`).
- Локально: `DATABASE_URL=postgres://... HTTP_ADDR=:8080 make run`.
- Тесты: `make test` (юнит + интеграция с testcontainers, нужен Docker).
- Линт: `make lint` (golangci-lint скачивается в `./bin`).
- Нагрузка: `make loadtest` — выделенное окружение (`deployments/docker-compose.loadtest.yml`), очистка БД и сценарии; результаты в [docs/loadtest.md](docs/loadtest.md).

### Конфигурация
- `DATABASE_URL` (обязательно), пример: `postgres://postgres:postgres@localhost:5432/prreviewer?sslmode=disable`
- `HTTP_ADDR` (по умолчанию `:8080`)

## Эндпоинты
- `POST /team/add`  
  Тело: `{"team_name": "...", "members": [{"user_id": "...", "username": "...", "is_active": true}]}`  
  Успех: 201 `{"team": {...}}`  
  Ошибки: 400 `TEAM_EXISTS`, 400 `BAD_REQUEST` при невалидном JSON/пустом team_name.

- `GET /team/get?team_name=...`  
  Успех: 200 объект команды. Ошибки: 400 при пустом team_name, 404 если не найдена.

- `POST /team/deactivate`  
  Тело: `{"team_name": "..."}`  
  Успех: 200 с агрегатами (`deactivated_users`, `reassigned_prs`, `failed_reassignments`, `deactivated_user_ids`).  
  Ошибки: 400 `BAD_REQUEST` при пустом имени, 404 если команда не найдена.

- `POST /users/setIsActive`  
  Тело: `{"user_id": "...", "is_active": true|false}`  
  Успех: 200 `{"user": {...}}`  
  Ошибки: 400 `BAD_REQUEST` при пустом user_id, 404 если пользователь не найден.

- `GET /users/getReview?user_id=...`  
  Успех: 200 `{"user_id": "...", "pull_requests": [...]}`  
  Ошибки: 400 при пустом user_id, 404 если пользователь не найден.

- `POST /pullRequest/create`  
  Тело: `{"pull_request_id": "...", "pull_request_name": "...", "author_id": "..."}`  
  Автоназначает до 2 активных ревьюеров из команды автора (исключая автора).  
  Успех: 201 `{"pr": {...}}`  
  Ошибки: 400 `BAD_REQUEST` при отсутствующих полях, 404 если нет автора/команды, 409 `PR_EXISTS`.

- `POST /pullRequest/merge`  
  Тело: `{"pull_request_id": "..."}`  
  Идемпотентно переводит PR в `MERGED`.  
  Успех: 200 `{"pr": {...}}`  
  Ошибки: 400 при пустом id, 404 если PR не найден.

- `POST /pullRequest/reassign`  
  Тело: `{"pull_request_id": "...", "old_user_id": "..."}`  
  Меняет ревьюера на случайного активного из его команды (исключая автора/уже назначенных).  
  Успех: 200 `{"pr": {...}, "replaced_by": "<new reviewer>"}`  
  Ошибки: 400 при пустых полях, 404 (PR/юзер), 409 `PR_MERGED`, `NOT_ASSIGNED`, `NO_CANDIDATE`.

- `GET /stats`  
  Успех: 200 с агрегатами по пользователям (назначения) и PR (OPEN/MERGED).  
  Ошибки: 500 — внутренняя.

- `GET /health`  
  Успех: 200 `{"status":"ok"}`.





## Тестирование и нагрузка
- `make test` — все юнит и интеграционные тесты (поднимут временный Postgres через testcontainers).
- `make coverage` — покрытие.
- `make loadtest` — отдельная БД для нагрузки; сценарии, RPS и латентности описаны в [docs/loadtest.md](docs/loadtest.md).

## Соответствие ТЗ
- Техническое задание: [Backend-trainee-assignment-autumn-2025.md](Backend-trainee-assignment-autumn-2025.md) (Avito), спецификация — [openapi.yml](openapi.yml).
- Идентификаторы (PR, пользователи, команды) задаёт клиент, как в ТЗ.
- Дополнительные задания реализованы: статистика, массовая деактивация с безопасным переназначением, нагрузочное тестирование (отчёт в [docs/loadtest.md](docs/loadtest.md)), интеграционные тесты, конфигурация линтера.
- Отклонений от требований ТЗ нет.
