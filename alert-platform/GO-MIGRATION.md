# Поэтапная миграция ядра на Go

## Решение

Целевая граница — доменное ядро на Go и адаптер TrueConf на Python.
Исходящая часть Python-адаптера не определяет получателей, не строит
шаблоны и не вызывает LLM: она получает готовую команду `DeliveryCommand
v1`, отправляет её через `python-trueconf-bot` и сохраняет идентификаторы
провайдера. Входящие vendor-события и команды (`/старт`, `/кабинет`,
`/алерты`, `/сводка`, reply-ACK) остаются рядом с TrueConf SDK на Python.

Gateway, pipeline worker, delivery planner, сценарии/SLA и admin API
перенесены в отдельные Go-процессы. Исходящий Python consumer по-прежнему
потребляет outbox-контракт и не импортирует маршрутизацию, шаблоны или
LLM-клиент.

## Контракт доставки

- JSON Schema: `contracts/delivery-command-v1.schema.json`.
- Хранилище демо-стенда: `delivery_outbox` в PostgreSQL.
- Публикация: одна транзакция с `notifications`.
- Конкурентное потребление: `FOR UPDATE SKIP LOCKED`.
- Восстановление: зависшие `processing` возвращаются по TTL.
- Ошибки: exponential backoff, затем статус `failed` после лимита попыток.
- Идемпотентность платформы: уникальный `idempotency_key` и одна команда
  на `notification_id`. TrueConf не предоставляет серверный idempotency key,
  поэтому авария после фактической отправки, но до commit остаётся редким
  at-least-once случаем и должна отслеживаться метрикой.

## Сверка с ТЗ кейса

| Требование | Архитектурное решение | Состояние |
|---|---|---|
| Несколько тысяч событий в сутки | Stateless Go gateway/planner, PostgreSQL queue, горизонтальные worker-реплики | Gateway, pipeline и delivery planner перенесены |
| Новые источники/каналы без изменения ядра | YAML-коннекторы и версионированный delivery-контракт | Соответствует |
| Экономически обоснованная инфраструктура | PostgreSQL outbox вместо отдельного брокера на пилоте | Соответствует |
| TrueConf обязателен | Vendor-библиотека изолирована в Python | Соответствует |
| LDAP/AD | Авторизация остаётся в admin API; периодическая сверка вынесена в изолированный `deprovision-worker` | Сохранено без зависимости доменного pipeline от LDAP |
| Универсальный API | Совместимый `POST /api/v1/ingest/raw`, `/openapi.json`, `/docs` | Перенесён на Go |
| Надёжность и трассировка доставки | Notification + outbox + provider IDs + retry | Реализовано |
| Локальный ИИ без внешнего облака | Ollama остаётся внутри контура; классификация/дедуп/root-cause на первом вхождении события выполняются синхронно в pipeline worker (добавляют задержку приёма, учтено в измеренном throughput в `INFRASTRUCTURE.md`), но вызовы best-effort — таймаут/ошибка не блокирует и не роняет обработку (раздел И5); доставка (planner → outbox → TrueConf) саму отправку от ответа ИИ не зависит | Сохраняется |
| Импортозамещение | TrueConf; совместимость PostgreSQL → Postgres Pro; стандартные HTTP/SQL-контракты | Сохраняется |

## Открытые расхождения с максимальной оценкой по ТЗ

- Индивидуальные токены источников (`X-Source-Token`) и rate limiting на
  логин уже реализованы и проверены вживую — подробности в `SECURITY.md`.
  Полного mTLS/allow-list по IP всё ещё нет — следующий шаг для
  продакшн-периметра за пределами пилота.
- Канонический `POST /api/v1/events` ещё не перенесён/реализован; доступен
  универсальный L0 endpoint `/api/v1/ingest/raw`.
- Reply на NEW уже фиксирует реакцию и используется Go-сценариями. Полная
  семантика действий `взял`/`не мой`/`следствие` относится к M8 и пока не
  замыкает обратную связь в Go core.
- «Умная маршрутизация на основе истории» (ИИ-сценарий) реализована в Go
  (`internal/adminapi/subscription_suggestion.go`) — раздел 5
  `ARCHITECTURE.md`.

## Очерёдность дальнейшего переноса

1. Добавить входящий контракт TrueConf → Go для полной семантики команд
   `взял`, `не мой`, `следствие`; Python преобразует vendor event в
   доменное событие.

## Завершённый этап: pipeline

- YAML-коннекторы и все существующие parser fixtures читаются Go-кодом.
- Resolver сохраняет каскад alias → FQDN → IP → fuzzy → quarantine.
- State manager сохраняет repeat/flapping/TTL и трассировку Event → Problem.
- Коррелятор сохраняет правило `corr-114`, состав Incident и защиту root.
- Приоритет считается по той же YAML-матрице с JSON breakdown.
- ИИ-классификация, cross-source dedup и гипотеза первопричины вызывают
  локальный Ollama best-effort и не блокируют конвейер при отказе.
- Сквозной тест на PostgreSQL подтверждён: ingest → alias resolve → P2 Problem
  → RESOLVED той же Problem; switch→host сформировал Incident по `corr-114`.

## Текущий этап: admin API

Go `admin-console` является внешним сервисом на `:8090`, раздаёт
существующий React SPA, сам выполняет LDAP bind и подписывает HttpOnly
session-cookie. В Go перенесены все endpoints SPA: dashboard summary,
incidents, alerts, equipment, employees/availability, scenarios, SLA и
integrations. Запись availability и audit выполняется одной транзакцией.

Server-rendered admin-страницы заменены React-экранами источников, аудита и
демо-сценариев; старые `/dashboard`, `/sources/`, `/audit/` и `/demo`
постоянно перенаправляются на их SPA-эквиваленты. Token-based личный кабинет
`/me/{username}/` перенесён в Go. Python `legacy-console` удалён из Compose;
изолированный `demo-runner` оставлен только для datagen и живых AI-проверок.

Полная схема зафиксирована в `database/migrations/0000_baseline.sql`.
Runtime-сервисы и seed-скрипт больше не вызывают `Base.metadata.create_all`;
на существующей БД baseline применяется идемпотентно, а на чистой создаёт
все 23 доменные таблицы до последовательного применения следующих миграций.

## Дополнительные Go-бинарники (история изменений)

Помимо перечисленных выше, Compose собирает и запускает ещё три Go-
бинарника из того же `go-platform/`: `kb-index` (одноразовая индексация
базы знаний), `clickhouse-migrate` (одноразовое применение схемы
ClickHouse) и `changelog-worker` (постоянный процесс — перенос истории
изменений в Kafka/ClickHouse и опциональная архивация в MinIO). Все три
— строго побочные, не входят в критический путь приёма/доставки.
Подробности — раздел 7 `ARCHITECTURE.md`.

## Локальный запуск текущего этапа

```bash
# В этой рабочей среде 5433 занят другим проектом.
POSTGRES_PORT=55433 docker compose up -d --build postgres migrate gateway pipeline-worker delivery-planner

curl http://127.0.0.1:8081/api/v1/health
open http://127.0.0.1:8081/docs
```

Полный стек требует заполненных `TRUECONF_*` из `.env`; без реальной
учётной записи TrueConf поднимать delivery-сервис бессмысленно.

## Критерий безопасного переключения стадии

- одинаковый JSON-контракт;
- одинаковый результат на fixtures и golden tests;
- shadow-прогон на копии событий без записи бизнес-состояния;
- отсутствие регрессии по latency, queue depth и ошибкам;
- rollback — возврат команды Compose на предыдущую реализацию.
