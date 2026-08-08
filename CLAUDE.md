# Контекст для Claude: продолжение разработки на Go

## Главное решение

Проект раньше был преимущественно на Python, но доменное и нагруженное
ядро уже перенесено на Go. Все новые изменения в основном runtime-тракте
нужно продолжать на Go, а не возвращать логику в старые Python-сервисы.

Рабочий каталог платформы: `alert-platform/`.

Перед изменениями прочитай:

1. `ALERT-PLATFORM-SPEC.md` — требования продукта.
2. `alert-platform/ARCHITECTURE.md` — текущая архитектура.
3. `alert-platform/GO-MIGRATION.md` — граница Go/Python и известные
   ограничения миграции.
4. `alert-platform/docker-compose.yml` — фактические runtime-процессы.

## Кто чем владеет

| Область | Текущая реализация | Где менять |
|---|---|---|
| Приём событий | Go | `alert-platform/go-platform/internal/gateway/` |
| Parser, resolver, state, correlation, priority | Go | `alert-platform/go-platform/internal/pipeline/` |
| Маршрутизация уведомлений, шаблоны, LLM-вызовы, outbox producer | Go | `alert-platform/go-platform/internal/planner/` |
| Сценарии и SLA | Go | `internal/scenario/`, `internal/planner/automation.go`, `internal/adminapi/platform.go` |
| Admin API, LDAP-сессия, личный кабинет и алерты | Go | `alert-platform/go-platform/internal/adminapi/` |
| Web UI | React/TypeScript | `alert-platform/web/` |
| Подключение к TrueConf, входящие команды и физическая отправка | Python | `alert-platform/services/delivery_trueconf/` |
| Миграции БД | SQL + Python runner | `alert-platform/database/migrations/`, `scripts/migrate.py` |
| Синтетическое демо и LDAP deprovision sweep | Python | `services/demo/`, `services/deprovision/` |

Compose запускает Go-бинарники `gateway`, `pipeline-worker`,
`delivery-planner` и `admin-console`. Одноимённые старые Python-модули могут
оставаться для тестов, совместимости и истории, но они не являются местом
для новой production-логики.

## TrueConf намеренно остаётся на Python

Не переписывай TrueConf SDK-слой на Go без отдельного архитектурного
решения. Используется специализированная библиотека `python-trueconf-bot`.

Python здесь отвечает за:

- авторизацию и соединение с TrueConf — `adapter.py`;
- vendor event types и команды `/старт`, `/кабинет`, `/алерты`, `/сводка`,
  а также reply-ACK — `handlers.py`;
- создание личного чата и отправку готовых команд из `delivery_outbox`,
  retry/backoff и сохранение `chat_id`/`message_id` — `outbox.py`.

Python TrueConf consumer не должен заново считать маршрутизацию, сценарии,
SLA или формировать исходящие доменные уведомления. Это делает Go planner,
который публикует версионированный `DeliveryCommand v1` в PostgreSQL outbox.

Если входящее действие TrueConf должно менять доменное состояние, оставь
разбор vendor-события в Python, а бизнес-правило реализуй в Go через явный
контракт. Текущий reply-ACK уже сохраняет реакцию в общей БД и используется
Go-сценариями.

## Правила изменений

- Не добавляй новую backend-функциональность в `services/api/main.py`,
  `services/gateway/main.py` или `services/pipeline/worker.py`, если её
  обслуживает соответствующий Go-процесс.
- Сохраняй существующие HTTP/JSON-контракты React UI и интеграций.
- Для очередей используй транзакции и `FOR UPDATE SKIP LOCKED`; обработка
  должна быть идемпотентной и безопасной для нескольких реплик.
- ИИ работает best-effort через локальный Ollama и не должен блокировать
  основной pipeline или доставку при ошибке/таймауте.
- Не вызывай `Base.metadata.create_all()` из runtime-кода. Источник истины
  схемы — последовательные SQL-миграции.
- Новую миграцию добавляй отдельным номером, делай повторное применение
  безопасным и проверяй как на существующей, так и на чистой PostgreSQL БД.
- Не удаляй и не заменяй Python TrueConf-адаптер заглушкой: без реального
  vendor SDK доставка и команды бота не работают.
- Не делай force-push и не затирай чужие изменения. Перед отправкой получи
  свежий `origin/main`, при необходимости выполни обычный rebase и аккуратно
  перенеси новую Python-логику в соответствующий Go-компонент.

## Проверки перед завершением

Из `alert-platform/`:

```bash
python3 -m compileall -q packages services scripts tests
python3 -m pytest -q
npm --prefix web run build
docker compose config -q
```

Из `alert-platform/go-platform/`:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
```

Для изменений схемы или planner дополнительно проверь миграции на чистой
временной PostgreSQL БД и фактические строки `notifications` +
`delivery_outbox`. Для admin API после сборки проверь LDAP login и основные
endpoint'ы (`analytics`, `scenarios`, `sla-rules`, `incidents`, `equipment`).

## Текущее состояние

- Go runtime, Python-тесты и React build проходят.
- Сценарии с ветвлением, reply-ACK и SLA исполняются Go planner'ом.
- NEW-уведомление и команда `/алерты` дают token-based ссылку на личный
  список текущих алертов.
- Полная семантика TrueConf-команд `взял`/`не мой`/`следствие` ещё не
  реализована; это следующий отдельный этап, а не повод возвращать planner
  на Python.
