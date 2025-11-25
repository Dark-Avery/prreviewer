# Load test

Инструмент: `tools/loadtest.go`.

Как гонять:
- Быстрый путь: `make loadtest` (соберёт образ, поднимет окружение, дождётся `/health`, прогонит `RESET_DB=1`, остановит и удалит контейнеры/volume).
- Ручной путь: `docker compose -f deployments/docker-compose.loadtest.yml up -d`, затем `BASE_URL=http://localhost:8080 WARMUP=1s DURATION=5s CONCURRENCY=20 RESET_DB=1 docker compose -f deployments/docker-compose.loadtest.yml exec app_loadtest /app/loadtest`, в конце `docker compose -f deployments/docker-compose.loadtest.yml down -v`.
- Сценарии по 5 секунд: health, stats, team_add, team_get, pr_create, mixed. Метрики: RPS, min/max/p50/p95/p99 latency, статус-коды, ошибки.

Результаты (inside compose, RESET_DB=1, WARMUP=1s, DURATION=5s, CONCURRENCY=20, BASE_URL=http://localhost:8080):

- health: Total 60 600, Errors 36 (timeout), RPS ~12 118, latency min 0.008ms / max 8.82ms / p50 1.72ms / p95 3.78ms / p99 4.89ms
- stats: Total 45 914, Errors 34 (timeout), RPS ~9 182, latency min 0.001ms / max 22.47ms / p50 2.44ms / p95 4.08ms / p99 5.07ms
- team_add: Total 17 964, Errors 40 (timeout), RPS ~3 593, latency min 1.54ms / max 75.00ms / p50 6.67ms / p95 8.60ms / p99 9.91ms (все успешные — 201)
- team_get: Total 58 326, Errors 33 (timeout), RPS ~11 664, latency min 0.022ms / max 16.43ms / p50 1.87ms / p95 3.43ms / p99 4.37ms (200)
- pr_create: Total 16 700, Errors 36 (timeout), RPS ~3 339, latency min 0.084ms / max 45.57ms / p50 6.76ms / p95 9.04ms / p99 10.81ms (201)
- mixed: Total 7 048, Errors 40 (timeout), RPS ~1 409, latency min 0.062ms / max 106.36ms / p50 5.90ms / p95 59.10ms / p99 67.11ms

Ошибки сведены к таймаутам клиента на закрытии окна теста.
