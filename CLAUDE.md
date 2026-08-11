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

## CODE QUALITY RULES

Перед реализацией любой задачи:

1. Определи слой системы (transport / application / domain / repository /
   external adapter) — см. `alert-platform/ARCHITECTURE.md`.
2. Найди существующую реализацию той же ответственности. Не создавай
   второй механизм там, где можно расширить существующий (пример из
   этой сессии: единый `FilterNode` AST для панели фильтров и Query
   Builder — не два параллельных пути фильтрации).
3. Не помещай business logic (routing, priority, SLA, correlation,
   availability, permission-вычисление, AI tool execution) в HTTP
   handler — handler только парсит запрос, проверяет auth/permission,
   вызывает use case, сериализует ответ.
4. Не помещай business logic во frontend — frontend адаптирует
   интерфейс под уже вычисленные backend'ом данные/permissions, не
   принимает решений сам (см. `internal/adminapi/rbac.go`: permission
   проверяется на сервере при каждом запросе, а не скрытием кнопки).
5. Не смешивай DAL и domain — SQL-текст живёт в repository-функциях,
   не внутри вычислений бизнес-правила.
6. Не переноси Go business logic обратно в Python. TrueConf-адаптер
   остаётся тонким vendor-слоем (см. раздел «TrueConf намеренно
   остаётся на Python» выше) — разбор входящего события можно оставить
   в Python, само бизнес-правило — только в Go через явный контракт.
7. Не обходи существующие domain contracts (`DeliveryCommand v1`,
   `FilterNode`, `rbac.Grant`, `changelog.Event`) — расширяй их, а не
   создавай параллельные структуры с тем же смыслом.
8. После изменения запусти quality pipeline (`make check` — быстрый,
   `make quality` — полный, см. `alert-platform/Makefile`) перед тем,
   как считать задачу завершённой.

Definition of Done для любого изменения — не «компилируется» и не
«визуально работает», а: функциональность работает + архитектурные
границы соблюдены + formatting/lint/static analysis проходят + unit/
integration/необходимые E2E тесты проходят + build проходит + нет
необоснованного дублирования + ошибки обработаны (не `_ = err`, кроме
явно документированных best-effort мест, например AI-обогащения).

## Проверки перед завершением

Быстрая проверка (соответствует `make check`) из `alert-platform/`:

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

Полная проверка (`make quality`) дополнительно включает `golangci-lint`,
ESLint, PostgreSQL integration-тесты на временной БД и migration-тесты —
см. `alert-platform/Makefile` и `alert-platform/.golangci.yml`. Полный
набор тяжелее и не обязателен на каждой мелкой правке, но обязателен
перед тем, как считать крупную фичу или PR завершённой.

Для изменений схемы или planner дополнительно проверь миграции на чистой
временной PostgreSQL БД и фактические строки `notifications` +
`delivery_outbox`. Для admin API после сборки проверь LDAP login и основные
endpoint'ы (`analytics`, `scenarios`, `sla-rules`, `incidents`, `equipment`).

## Известный технический долг (Quality Gate)

`golangci-lint run ./...` из `alert-platform/go-platform/` осознанно не
доведён до нуля находок. Из 74 первоначальных находок исправлено 53
(unused, errorlint, errcheck, contextcheck, gosec — все настоящие
false positives задокументированы прямо в `.golangci.yml` рядом с
исключением, а не молча подавлены; funlen — 2 функции разбиты на
подфункции). Осознанно НЕ исправлено:

- **20 находок `gocyclo`** (cyclomatic complexity > 15, до 136 у
  `(*Server).ServeHTTP` и 56 у `(*Planner).advanceScenario`) — это
  большие, работающие, уже покрытые тестами функции маршрутизации и
  бизнес-логики (`ServeHTTP`, `routeScenarios`, `routeGroups`,
  `advanceScenario`, `scenario.Parse` и т.д.). Рефакторинг ради снятия
  одной lint-метрики под давлением времени этой итерации создаёт
  реальный риск регрессии, несоразмерный пользе. Если кто-то берётся
  за это отдельно — начинать с `ServeHTTP` (разбить на под-роутеры по
  префиксу пути, часть уже вынесена в `routeGroups`/`routeScenarios`/
  `routeEmployeeAvailability`) и `advanceScenario` (вынести резолюцию
  графа и переходы по типам узлов в отдельные функции).
- **1 находка `staticcheck` QF1003** (`bi_routes.go:167`) — предложение
  переписать `if/else` на `switch` по одной переменной, чистый стиль
  без функционального эффекта.

Это состояние, не задача с открытым концом: `make quality` в CI должен
считать эти 21 находку baseline, а не блокировать сборку — см.
`alert-platform/.golangci.yml`.

## Текущее состояние

- Go runtime, Python-тесты и React build проходят.
- Сценарии с ветвлением, reply-ACK и SLA исполняются Go planner'ом.
- NEW-уведомление и команда `/алерты` дают token-based ссылку на личный
  список текущих алертов.
- Полная семантика TrueConf-команд `взял`/`не мой`/`следствие` ещё не
  реализована; это следующий отдельный этап, а не повод возвращать planner
  на Python.
