# Документация проекта — ADP (Alert Data Platform / «Диспетчер»)

Единая техническая документация для разработчика или проверяющего,
незнакомого с кодовой базой. Не заменяет специализированные документы
(`ARCHITECTURE.md`, `SECURITY.md`, `INFRASTRUCTURE.md` и др.) — сводит их
в одну карту и полностью описывает то, что появилось позже них и ещё не
задокументировано отдельно (ADP AI, дерево организации, RBAC, integration-
тесты). Формальные документы по официальному шаблону кейса (модель
ролей, безопасность, изоляция АСУ ТП, лицензирование, экономика) — в
`docs/*.docx`, это отдельный комплект, не пересказывается здесь.

## 1. Что это

ADP — платформа сбора, разбора, корреляции и маршрутизации алертов
промышленного мониторинга (Zabbix/SolarWinds) с доставкой в TrueConf и
email, self-service подписками, конструктором сценариев реагирования,
SLA и встроенным ИИ-ассистентом (ADP AI). Полная продуктовая
спецификация — `ALERT-PLATFORM-SPEC.md`.

## 2. Архитектура в двух словах

Go — приём, разбор, корреляция, маршрутизация, сценарии, SLA, admin
API (нагруженное ядро). Python — тонкий TrueConf-адаптер (вендорный
SDK без Go-аналога), миграции, синтетическое демо. React/TypeScript —
консоль администратора. Полная схема компонентов, обмен сообщениями,
хранилища — `alert-platform/ARCHITECTURE.md`.

```
Источник (Zabbix/SolarWinds) → Gateway (Go) → signal_queue → Pipeline (Go:
разбор/резолвинг CMDB/дедуп/корреляция/приоритет) → problems/incidents →
Planner (Go: сценарии/SLA/маршрутизация) → delivery_outbox →
  ├─ delivery-trueconf (Python, вендорный SDK)
  └─ delivery-email (Go)
Admin API (Go) обслуживает React-консоль + ADP AI (Ollama, best-effort)
```

## 3. Карта модулей

### Go (`go-platform/`) — основной нагруженный тракт

| Пакет | Назначение |
|---|---|
| `cmd/gateway` | HTTP-приём событий от источников (`POST /api/v1/ingest/raw`), `X-Source-Token` |
| `cmd/pipeline-worker` | Разбор/резолвинг/дедуп/корреляция/приоритет — превращает Signal в Problem |
| `cmd/delivery-planner` | Сценарии, SLA-каскад, маршрутизация, producer для `delivery_outbox` |
| `cmd/delivery-email` | Второй канал доставки — тонкий claim/retry поверх `delivery_outbox`, open-пиксель/click-трекинг |
| `cmd/admin-api` | HTTP API для React-консоли: LDAP-сессия, все `/api/*` эндпоинты, ADP AI |
| `cmd/changelog-worker` | Реле `change_events` → Kafka(Redpanda) → ClickHouse (История изменений) |
| `cmd/clickhouse-migrate` | Миграции схемы ClickHouse (аналитический сток, побочный контур) |
| `cmd/kb-index` | Одноразовая индексация `knowledge_base/*.md` → pgvector (RAG) |
| `internal/gateway` | Логика приёма/валидации источника |
| `internal/pipeline` | Парсер коннекторов (`connectors/*.yaml`), резолвер CMDB, дедуп/корреляция, приоритет, ИИ-нормализация |
| `internal/planner` | Sceanario-планировщик, SLA, Ollama-клиент, on-demand ИИ-разбор (RAG), knowledge base chunking |
| `internal/scenario` | Парсер/движок графа сценария (condition→notify→wait→ack_check→escalation) |
| `internal/availability` | Единственная реализация приоритета пересекающихся интервалов доступности сотрудника |
| `internal/coverage` | Sweep разрывов покрытия по политике (переиспользуется Покрытием, Главной, ADP AI) |
| `internal/rbac` | Модель роли+grant/deny+scope; единственный источник правды по правам |
| `internal/changelog` | Global Audit (`change_events`) + ClickHouse-реле + data lake (MinIO) |
| `internal/deliveryemail` | SMTP-доставка + tracking-токены |
| `internal/adminapi` | Все HTTP-хендлеры консоли: инциденты, алерты, оборудование, сотрудники, org_units, группы, сценарии, SLA, аналитика, аудит, ADP AI, RBAC |
| `internal/testutil` | `//go:build integration` — testcontainers-помощник для integration/migration тестов |

### Python

| Каталог | Назначение |
|---|---|
| `services/delivery_trueconf/` | Единственный вендорный слой — TrueConf bot (`python-trueconf-bot`): исходящие команды из outbox, `/старт /кабинет /алерты /сводка`, reply-ACK, `анализ` |
| `services/demo/` | Синтетическое демо (`datagen/*.yaml` → `/api/v1/ingest/raw`), живые кнопки `/console/demo` |
| `services/deprovision/` | Sweep увольнений: подписчик без соответствия в LDAP — деактивируется |
| `services/api`, `services/gateway`, `services/pipeline`, `services/router`, `services/web` | Исходные Python-реализации до Go-миграции — оставлены для истории/тестов, НЕ место для новой production-логики (см. `CLAUDE.md`) |
| `packages/common` | Общие модели/утилиты (LDAP-аутентификация, ingest, sources) |
| `packages/ai` | ИИ-классификация/дедуп/корреляция (вызывается и Python-, и Go-слоем через общий Ollama HTTP-контракт) |
| `packages/rules` | `priority_matrix.yaml` — матрица приоритезации |
| `packages/scenarios`, `packages/graphing`, `packages/models` | Вспомогательные модули для сценариев/графов/типов |
| `scripts/` | `migrate.py` (runner миграций), `seed_demo.py`/`seed_employees.py`/`seed_demo_scenarios.py`/`seed_scenario_templates.py` (демо-данные, идемпотентны) |

### Web (`web/`) — React/TypeScript SPA консоли

Разделы, соответствующие целевой навигации (`web/src/components/Sidebar.tsx`):
Главная, Алерты, Инциденты, Оборудование (дерево), Покрытие, Сотрудники
(дерево организации), Календарь, Группы, Сценарии, SLA, **ADP AI**,
История изменений, Аналитика, Интеграции, Источники, Пользователи и
права, Аудит, Состояние системы, Справка. Каждая страница адаптирует
интерфейс под уже вычисленные backend'ом permissions/данные, сама не
принимает решений (см. `CLAUDE.md`, раздел CODE QUALITY RULES).

## 4. Доменная модель

Ключевая цепочка сущностей (детали — `ARCHITECTURE.md`, `ALERT-PLATFORM-SPEC.md`):

```
Signal (сырое сообщение источника, дедуп по hash)
  → Event (разобранное, привязано к CMDB через resolver)
    → Problem (дедуп по dedup_key = site|object|component|symptom;
               repeat_count при повторе, не новая строка)
      → Incident (кластер коррелированных Problem; closed_at IS NULL —
                   единственный источник правды «открыт/закрыт», см. §6)
        → Notification → delivery_outbox → TrueConf/Email
```

Справочники: `cmdb_objects`/`cmdb_aliases` (оборудование), `subscribers`
(сотрудники), `org_units` (дерево организации, произвольная глубина —
раздел 7), `groups`/`group_equipment_scope` (зоны ответственности),
`scenarios`/`scenario_runs`/`scenario_run_steps` (граф реагирования),
`sla_rules`/`sla_breach_notices`, `employee_availability` (типизированные
интервалы доступности с приоритетом при пересечении — `internal/availability`),
`coverage_policies` (политики покрытия).

Полная история схемы — 22 файла `database/migrations/0000_baseline.sql`
… `0021_ai_journal.sql`, каждый идемпотентен (`IF NOT EXISTS`), проверено
integration-тестом на чистой и «существующей» БД (§8).

## 5. RBAC — модель прав

`go-platform/internal/rbac/rbac.go` — единственный источник правды.
Формула: `Effective = RolePermissions[role] + individual grants − individual
denies`, посчитана на сервере при каждом запросе (`withPermission`), не
скрытием кнопки во фронтенде.

- **7 фиксированных ролей**: `platform_admin` (всё), `dispatcher`,
  `engineer`, `service_owner`, `automation_manager`, `auditor`, `guest`
  (не назначаемая — отдельный тип сессии, не запись в `platform_users`).
- **Scope данных**: филиал/подразделение/сервис/тип оборудования/конкретный
  объект — сужает видимость поверх permission, не заменяет её.
- **Индивидуальные overrides**: `user_permission_overrides` — то, чем
  реально управляет экран «Пользователи и права».
- **Бутстрап**: первый LDAP-логин создаёт `platform_users` с ролью
  `engineer` (или `platform_admin` при членстве в LDAP-группе `admins`);
  дальше роль/права меняет администратор.
- **Аудит отказа**: с этой итерации `withPermission` при 403 пишет
  `audit_log(action='permission_denied')` — раньше отказ был не виден в
  аудите вообще (обрывался до хендлера, который пишет audit_log).

Полный живой пример (реальный тестовый аккаунт `тестер1`) — раздел 7
этого документа и `Критерии_оценки/5_Информационная_безопасность/`.

## 6. Инциденты — реальный жизненный цикл (важная деталь для тех, кто читает код)

У `incidents` нет собственного статуса — только `closed_at`
(`NULL` = открыт). Закрывается **только** когда ВСЕ Problem-участники
кластера реально `RESOLVED` (`internal/pipeline/state.go::closeIncidentIfAllResolved`),
не когда резолвится один член. До аудита в этой итерации
`GET /api/incidents?status=` ошибочно фильтровал по `root.status`
(статусу ОДНОЙ проблемы) — инцидент с резолвнутым root, но открытыми
сиблингами, попадал бы во вкладку «Завершённые», хотя `closed_at` ещё
`NULL`. Исправлено — фильтр теперь по `incidents.closed_at` в SQL WHERE.

## 7. ADP AI — ассистент поверх данных платформы

Отдельный модуль (`internal/adminapi/ai_tools.go`, `ai_routes.go`,
`ai_journal.go`; фронтенд `web/src/pages/AdpAi.tsx`). Ключевое
архитектурное ограничение, проверяемое буквально: **у LLM нет прямого
доступа к PostgreSQL/ClickHouse/shell**.

```
Пользователь → текст на естественном языке
  → Ollama выбирает ОДИН инструмент из реестра + параметры (строгий JSON)
    → реестр валидирует имя инструмента и параметры
      → проверка права ЭТОГО пользователя (rbac.Grant, не отдельный AI-грант)
        → Use Case (переиспользует существующие запросы — FilterNode,
          equipmentResponsibleGroups, availability.Resolve, coverage.Sweep)
          → Repository (реальный SQL)
            → шаблонный ответ из структурных данных (не LLM-парафраз)
              → ai_journal (каждый запрос) + change_events (значимые действия)
```

**Реестр инструментов** (все — read/navigate, MVP осознанно без write —
см. `Критерии_оценки/3_Использование_инструментов_ИИ/`):

| Инструмент | Право | Переиспользует |
|---|---|---|
| `list_active_incidents` | `incidents.read` | тот же `incidents.closed_at IS NULL`, что и вкладка «Активные» |
| `get_incident` | `incidents.read` | — |
| `find_alerts` | `alerts.read` | `FilterNode`/`compileFilterNode` — тот же Query Builder, что у панели фильтров алертов |
| `find_equipment` | `equipment.read` | — |
| `get_available_responders` | `employees.read` | `equipmentResponsibleGroups` + `availability.Resolve` — те же, что карточка оборудования и раздел «Покрытие» |
| `get_coverage` | `coverage.read` | `coverage.Sweep` |
| `get_analytics` | `analytics.read` | `loadNoiseFunnel` (тот же расчёт, что «Аналитика») |
| `open_entity` | право чтения нужного типа (динамически) | структурная навигация `{type,id}`, маршрут строит фронтенд — никогда LLM-URL |

**Защита от галлюцинаций (MVP)**: ответ пользователю собирается
шаблонно из структурных данных инструмента, LLM только выбирает, ЧТО
спросить — не формулирует финальный текст с фактами. Проверено живьём:
запрос «активные P0» при их фактическом отсутствии в БД дал «не
найдено», не выдумку.

**Деградация при недоступности Ollama**: отдельное сообщение «ADP AI
временно недоступен», остальная платформа продолжает работать
(`server.ollama == nil`, а также `Ask()==nil` при таймауте/недоступности
— обе ветки отдельно проверены, вторая была реальным багом, найденным и
исправленным при живой проверке: код путал «не ответила» с «ответила,
но не выбрала инструмент»).

**Журнал** (`ai_journal`) — отдельная запись на КАЖДЫЙ запрос
(успешный и нет: request_text/tool_name/status/duration_ms/model/
error_code). Значимые (не navigate) действия ДОПОЛНИТЕЛЬНО пишутся в
существующий Global Audit (`change_events`, поля `actor_type='ai'`,
`initiated_by=<пользователь>`) — цепочка user→AI→tool→результат видна
в одном месте, не в параллельном учёте.

**Постоянный принцип прав**: ADP AI никогда не имеет больше прав, чем
вызывающий пользователь — каждый tool проверяет `rbac.Grant` этого
пользователя, не отдельную AI-роль. Проверено живьём: `тестер1` без
`employees.read` получил `DENIED` от `get_available_responders`; после
выдачи права тот же запрос прошёл.

## 8. Тестирование

| Уровень | Где | Как запустить |
|---|---|---|
| Юнит (Go) | `internal/*/*_test.go` | `cd go-platform && go test ./...` (без Docker) |
| Юнит (Python) | `tests/*.py` | `python3 -m pytest -q` |
| **PostgreSQL integration** (новое) | `internal/pipeline/integration_test.go`, `internal/adminapi/integration_test.go` | `go test -tags=integration ./internal/pipeline/... ./internal/adminapi/...` (нужен Docker — реальный `pgvector/pgvector:pg16` через testcontainers-go, не мок) |
| **Migration** (новое) | `internal/testutil/migrations_test.go` | `go test -tags=integration ./internal/testutil/...` — все 22 файла на чистой БД, затем повторно (идемпотентность) |
| E2E/демо | Живая проверка на проде | `Критерии_оценки/README.md`, раздел «Живой демо-сценарий» |
| CI | `.github/workflows/ci.yml` | Джобы `go`, `postgres-integration`, `python`, `web`, `compose` — на каждый push/PR |

Что проверяют integration-тесты конкретно: реальный цикл
ingest→resolve→dedup→repeat_count→RESOLVED через `Service.Tick`;
`FOR UPDATE SKIP LOCKED` — вторая транзакция не видит строку, залоченную
первой (multi-replica safety, CLAUDE.md); `resolveGrant` — bootstrap
нового пользователя + individual override, тот же путь, что реально
использовался для `тестер1`.

`make check` (быстро, без Docker) vs `make quality` (полно, включает
`pg-integration-test`/`migration-test`) — `alert-platform/Makefile`.

## 9. Деплой и эксплуатация

Полные инструкции — `README.md` («Воспроизводимый запуск», «Как
проверить, что всё работает») и `Критерии_оценки/README.md` (быстрый
доступ + тестовые учётные записи для проверяющих). Живой стенд:
`https://газвпол.рус/console/app/`.

## 10. Глоссарий

| Термин | Значение |
|---|---|
| Signal | Сырое сообщение от источника мониторинга, как оно пришло |
| Event | Signal после разбора коннектором и резолвинга объекта CMDB |
| Problem | Дедуплицированная проблема (один открытый экземпляр на `dedup_key`) |
| Incident | Кластер коррелированных Problem с общей первопричиной |
| Source instance | Зарегистрированный инстанс системы мониторинга (Zabbix/SolarWinds) с собственным `X-Source-Token` |
| Connector | YAML-описание формата сообщений источника (`connectors/*.yaml`) — конфигурация, не код |
| Scenario | Граф автоматического реагирования (condition→notify→wait→ack_check→escalation) |
| Coverage policy | Правило «в группе X должно быть доступно не менее N человек» |
| Org unit | Узел дерева организации произвольной глубины (раздел 7 доп. ТЗ) |
| ADP AI | Встроенный ИИ-ассистент поверх данных платформы (раздел 7 этого документа) |
| Grant | Вычисленный эффективный набор прав конкретной сессии (`rbac.Grant`) |

## 11. Карта остальной документации

| Документ | О чём |
|---|---|
| `ALERT-PLATFORM-SPEC.md` | Полная продуктовая спецификация |
| `alert-platform/ARCHITECTURE.md` | Компоненты, обмен сообщениями, хранилища |
| `alert-platform/SECURITY.md` | Угроза→мера→доказательство→тест→остаточный риск |
| `alert-platform/INFRASTRUCTURE.md` | Отказоустойчивость, производительность, ресурсы ИИ |
| `alert-platform/GO-MIGRATION.md` | Граница Go/Python, история миграции |
| `alert-platform/PYTHON_VS_GO.md` | Сравнение и мотивация выбора |
| `alert-platform/USER_GUIDE.md` | Обзор интерфейса + демо-доступы |
| `alert-platform/COMPLIANCE_MATRIX.md` | Построчное соответствие критериям кейса |
| `alert-platform/PRESENTATION_CONTENT.md` | Текст презентации, слайды, демо-скрипт, числа |
| `Критерии_оценки/` | Навигация для проверяющих по 8 официальным критериям + живой демо-сценарий |
| `docs/*.docx` | Официальный комплект по шаблону кейса (модель ролей, безопасность, изоляция АСУ ТП, сетевые взаимодействия, лицензирование, инфраструктура, концептуальное приложение) |
| `CLAUDE.md` | Правила для дальнейшей разработки (слои, границы ответственности, definition of done) |
