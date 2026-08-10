# Схема внешнего и сетевого взаимодействия платформы «Диспетчер»

## 1. Назначение документа и правила достоверности

Документ фиксирует внешние интеграции, межсервисные потоки и рекомендуемое сетевое размещение платформы «Диспетчер». Он предназначен для архитекторов, сетевых инженеров, ИБ, эксплуатации и приёмочной комиссии. Это не инструкция «открыть все перечисленные порты»: разрешения выдаются только для конкретных пар источник–получатель после подстановки утверждённых адресов.

Используются три обязательные метки:

* **ФАКТ** — подтверждается текущим репозиторием: кодом, `docker-compose.yml`, Dockerfile или существующей проектной документацией. Факт о демо-стенде не становится автоматически production-нормой.
* **ЦЕЛЕВОЕ** — рекомендуемое production-состояние, которого текущий Compose сам по себе не обеспечивает.
* **ТРЕБУЕТ СОГЛАСОВАНИЯ** — невозможно корректно определить без данных заказчика: IP/VLAN/VRF, корпоративной PKI, версии TrueConf, адресов AD, прокси, SIEM, backup-сети, RPO/RTO и правил промышленного периметра.

**ФАКТ.** Текущий `docker-compose.yml` — демонстрационное односерверное развёртывание. В нём есть открытые наружу host-порты, loopback-публикации, пароли по умолчанию, незашифрованные внутренние протоколы и один контейнер в `network_mode: host`. Поэтому этот файл нельзя выдавать за готовую production-сетевую схему. Разделы 8–13 задают целевую модель, которую надо реализовать средствами корпоративной сети, reverse proxy/API gateway, межсетевых экранов и secret management.

Источники: спецификация, архитектура, security/infrastructure, Compose/Dockerfile, код gateway/admin/planner/pipeline/changelog/TrueConf, connectors, LDAP, replication и Ollama-клиенты.

## 2. Контекстная схема

```mermaid
flowchart LR
    subgraph OT["Технологические и мониторинговые сегменты"]
        Z["Zabbix\n4 зарегистрированных инстанса"]
        S["SolarWinds\n4 зарегистрированных инстанса"]
        N["Новый источник\nпосле регистрации и паспортизации"]
    end

    subgraph EDGE["Integration DMZ — целевое"]
        IR["Ingress relay / API gateway\nTLS, mTLS, rate limit, spool"]
        RPX["Пользовательский reverse proxy\nHTTPS, WAF, security headers"]
    end

    subgraph APP["Контур приложения"]
        GW["Gateway, Go\nприём Signal"]
        PG[("PostgreSQL primary\nWORM, очереди, CMDB, outbox")]
        PW["Pipeline worker, Go"]
        DP["Delivery planner, Go"]
        AC["Admin Console\nGo API + React SPA"]
        DT["TrueConf adapter, Python"]
        CW["Changelog worker, Go"]
    end

    subgraph DATA["Контур данных и аналитики"]
        PGR[("PostgreSQL replica")]
        RK[("Redpanda")]
        CH[("ClickHouse")]
        MI[("MinIO")]
        OL[("Ollama\nлокальные модели")]
    end

    subgraph CORP["Корпоративные сервисы"]
        AD["LDAP / AD"]
        TC["TrueConf Server"]
        USERS["Браузеры сотрудников\nи администраторов"]
        OBS["Monitoring / SIEM"]
        BK["Backup repository"]
        SYS["DNS / NTP / PKI / proxy"]
    end

    Z --> IR
    S --> IR
    N --> IR
    IR --> GW
    GW --> PG
    PG <--> PW
    PG <--> DP
    PG <--> AC
    PG <--> DT
    PG --> PGR
    PW -.->|"best-effort HTTP"| OL
    DP -.->|"best-effort HTTP"| OL
    AC -.->|"best-effort HTTP"| OL
    PG --> CW
    CW --> RK
    RK --> CW
    CW --> CH
    CW --> MI
    DT <--> TC
    TC <--> USERS
    USERS --> RPX
    RPX --> AC
    AC --> AD
    SYS --> GW
    AC --> OBS
    PG --> BK

    classDef target stroke-dasharray:5 5,stroke:#666,fill:#f7f7f7;
    class IR,RPX,OBS,BK,SYS target;
```

Главный бизнес-поток односторонен по смыслу: источник мониторинга отправляет событие, платформа сохраняет оригинал, обрабатывает его и передаёт готовое уведомление в TrueConf. HTTP-ответ `202 Accepted` является только подтверждением записи, а не управляющей командой в сторону АСУ ТП. Пользовательская обратная связь приходит через TrueConf и меняет доменное состояние в общей БД. Redpanda, ClickHouse и MinIO находятся строго ниже основного потока: их отказ не должен останавливать ingest или доставку.

## 3. Границы доверия и классы данных

### 3.1. Границы доверия

| Граница | Что пересекает | Основной риск | Требуемый контроль |
|---|---|---|---|
| Источник/OT → integration DMZ | сырой текст алерта, идентификатор источника, время, внешний ID | подмена, replay, flood, перенос вредоносного содержимого, раскрытие технологических данных | исходящее соединение от источника; mTLS; индивидуальный token/HMAC; allow-list; schema/size/rate limit; локальный spool; журналирование без тела |
| Integration DMZ → application zone | проверенный конверт события | обход первой границы, lateral movement | повторное TLS до gateway; отдельные CA/credentials; нет L3-транзита; только один API path |
| User zone → application zone | HTTPS UI/API, session cookie, token-ссылки из TrueConf | кража сессии, brute force, CSRF/XSS, несанкционированная конфигурация | reverse proxy, TLS/HSTS, LDAP auth, HttpOnly/Secure/SameSite cookie, RBAC, rate limit, security headers, аудит |
| Application → directory/messaging | LDAP bind; TrueConf bot traffic | компрометация service account, перехват пароля/токена | LDAPS; least privilege; bot account; TLS validation; секреты вне Compose/Git; точечные firewall rules |
| Application → data/AI | SQL, Kafka, ClickHouse, S3, LLM prompts | массовая утечка, обход бизнес-правил, отравление аналитики | отдельные service accounts; TLS; ACL; deny direct user access; шифрование томов; audit; лимиты |
| Management/backup/observability | логи, метрики, дампы, обновления | привилегированный обход всех зон | отдельные management и backup VLAN; bastion; MFA; immutable backup; подписанные обновления; SIEM |

**ФАКТ.** В демо большинство контейнеров находятся в одной автоматически созданной Docker bridge-сети и доверяют её DNS-именам. Сетевой микросегментации между `gateway`, `postgres`, workers, LDAP, Redpanda, ClickHouse и MinIO в Compose нет. Loopback-bind защищает от прямого внешнего подключения к части host-портов, но не заменяет сервисные ACL внутри bridge-сети.

### 3.2. Данные в потоках

Служебные health/metrics не должны содержать секретов. Конфигурация, parser/rules и подписки требуют RBAC, версий и audit before/after. Исходные алерты, адреса оборудования, инциденты и действия сотрудников являются производственными/персональными данными: разрешены только в OT/DMZ/application/data/TrueConf по назначению, шифруются и не уходят во внешнее облако. Tokens, DB/LDAP/bot credentials, session secret и private keys хранятся только в vault/HSM/secret store, не в Git, image или обычных логах; для них обязательны ротация и отзыв.

## 4. Инвентаризация внешних систем

| Система | Статус | Роль и данные | Кто инициирует соединение | Ограничения |
|---|---|---|---|---|
| Zabbix | **ФАКТ** для пилота | отправляет plain-text `PROBLEM/RESOLVED`; четыре инстанса в `connectors/sources.yaml` | Zabbix action/webhook → gateway | текущий parser YAML использует regex; webhook URL и IP заказчика не заданы |
| SolarWinds | **ФАКТ** для пилота | отправляет plain-text `Alert/Reset`; четыре инстанса | SolarWinds action/webhook → gateway | parser YAML; endpoint/учётные данные заказчика не заданы |
| Иные источники | **ЦЕЛЕВОЕ** | спецификация перечисляет HTTP webhook, syslog, SNMP trap, AMQP, SMTP и API polling | зависит от адаптера | в текущем Go gateway реализован только HTTP JSON `POST /api/v1/ingest/raw`; остальные протоколы нельзя считать реализованными |
| Корпоративный LDAP/AD | **ЦЕЛЕВОЕ**; demo использует glauth | проверка пользователя, `memberOf`, деактивация подписок | admin-console и deprovision-worker → каталог | demo использует LDAP 389 без TLS; production DN, CA, HA и права не известны |
| TrueConf Server | **ФАКТ** как обязательный канал | аутентификация бота, WebSocket/event stream, отправка сообщений, входящие команды/reply | adapter → TrueConf; сервер возвращает события в установленной сессии | default в коде `4309`, HTTP/HTTPS управляется env; версия и реальный TLS-профиль не подтверждены |
| Браузер сотрудника/администратора | **ФАКТ** | SPA, API, кабинет/алерты по персональной token-ссылке | browser → admin-console через внешний URL | в Compose `8090` опубликован напрямую; production требует reverse proxy HTTPS |
| Корпоративные DNS/NTP/PKI | **ТРЕБУЕТ СОГЛАСОВАНИЯ** | разрешение имён, единое время, выпуск/отзыв сертификатов | все узлы/сервисы → утверждённые резолверы/реле | в репозитории нет production-конфигурации |
| SIEM/monitoring | **ЦЕЛЕВОЕ** | audit, availability, queue/outbox age, auth/TLS/network events | platform/collectors → monitoring/SIEM | централизованный exporter/agent в Compose отсутствует |
| Backup repository | частичный **ФАКТ** вне репозитория | ежедневный `pg_dump`, 7 дней; целевое immutable/off-host | backup agent → БД/repository | серверные `~/pg_backup.sh` и `~/pg_restore.sh` описаны, но в репозитории отсутствуют |
| Package/image repositories | **ФАКТ только build/update** | Docker Hub, PyPI, Go modules, npm registry | build host/proxy → repositories | runtime не должен иметь internet egress; требуется внутренний mirror и allow-list |

## 5. Инвентаризация внутренних компонентов

| Компонент | Слушает/подключается | Назначение и зависимость |
|---|---|---|
| `gateway` | слушает `:8080` в контейнере; host `8081`; подключается к `postgres:5432` | атомарно пишет `Signal` WORM и `signal_queue`; возвращает 202; лимит body 1 МиБ |
| `pipeline-worker` | входящего listener нет; PostgreSQL и Ollama 11434 | claim/process/sweep; parser, resolver, state, correlation, priority; Ollama best-effort |
| `delivery-planner` | listener нет; PostgreSQL и Ollama | маршрутизация, шаблоны, RAG/LLM, создание `delivery_outbox` |
| `delivery-trueconf` | host network; исходящие к PostgreSQL и TrueConf | единственный vendor SDK-слой; читает outbox, создаёт личные чаты, отправляет, принимает bot events |
| `admin-console` | контейнер/host `8090`; PostgreSQL, LDAP, ClickHouse, Ollama, demo-runner | SPA, session auth, admin API, персональные страницы, proxy demo routes |
| `demo-runner` | `8091` только в bridge-сети | синтетика и AI self-test; не production critical path |
| `deprovision-worker` | listener нет; PostgreSQL и LDAP | периодическая сверка учётных записей, деактивация подписок |
| `postgres` | `5432` в bridge; host loopback `5433` по умолчанию | source of truth, WORM, очереди, outbox, CMDB, audit, pgvector |
| `postgres-replica` | `5432` только в bridge | асинхронная физическая read-only replica; manual promotion |
| `ldap`/glauth | `389` в bridge; host loopback `3893` | только синтетический demo-каталог; LDAPS отключён |
| `redpanda` | internal `9092`; host loopback `19092` | `change_events.v1`, один broker, без auth в demo |
| `clickhouse` | native `9000`, HTTP `8123`; loopback `9010/8123` | аналитический low-code поиск; не source of truth |
| `minio` | S3 `9000`, console `9001`; loopback `9002/9003` | опциональный NDJSON-архив сырых Signal |
| `changelog-worker` | listener нет; PostgreSQL, Redpanda, ClickHouse, MinIO | три downstream-потока; не влияет на ingest/delivery |
| `migrate`, `clickhouse-migrate`, `kb-index` | одноразовые клиенты | SQL migrations, ClickHouse schema, embedding index; не постоянные runtime-сервисы |
| Ollama на Docker host | обычно `11434` | локальные `log-reader` и `nomic-embed-text`; Compose-сервисы идут через `host.docker.internal` |

## 6. Фактическая deployment/network-схема демонстрационного Compose

```mermaid
flowchart TB
    EXT["Сеть хоста / внешние клиенты"]
    LOOP["Loopback хоста 127.0.0.1"]
    DT["delivery-trueconf\nhost-network container"]
    OLL["Ollama на хосте :11434"]
    TCL["TrueConf Server на хосте :4309"]

    subgraph BR["Docker Compose bridge + service DNS"]
        GW["gateway :8080"]
        ADM["admin-console :8090"]
        DEMO["demo-runner :8091"]
        PIPE["pipeline-worker"]
        PLAN["delivery-planner"]
        DEP["deprovision-worker"]
        PG["postgres :5432"]
        REP["postgres-replica :5432"]
        LDAP["ldap :389"]
        RED["redpanda :9092 / :19092"]
        CH["clickhouse :9000 / :8123"]
        MIN["minio :9000 / :9001"]
        CW["changelog-worker"]
    end

    EXT -->|"0.0.0.0:8081 → 8080"| GW
    EXT -->|"0.0.0.0:8090 → 8090"| ADM
    LOOP -->|"3893 → 389"| LDAP
    LOOP -->|"5433 → 5432"| PG
    LOOP -->|"19092"| RED
    LOOP -->|"8123; 9010 → 9000"| CH
    LOOP -->|"9002 → 9000; 9003 → 9001"| MIN
    PIPE --> PG
    PIPE -->|"host.docker.internal:11434"| OLL
    PLAN --> PG
    PLAN -->|"host.docker.internal:11434"| OLL
    ADM --> PG
    ADM --> LDAP
    ADM --> DEMO
    ADM --> CH
    ADM -->|"host.docker.internal:11434"| OLL
    DEP --> PG
    DEP --> LDAP
    CW --> PG
    CW <--> RED
    CW --> CH
    CW --> MIN
    REP -->|"bootstrap/streaming session"| PG
    DT -.->|"БД: Docker service DNS отсутствует"| PG
    DT <-->|"127.0.0.1:4309 по замыслу стенда"| TCL
```

### 6.1. Фактическая публикация портов

| Compose service | Container listener | Host exposure | Доступность с другой машины по одному Compose | Вывод |
|---|---:|---:|---|---|
| gateway | 8080/TCP | `0.0.0.0:8081` из записи `8081:8080` | да, если не блокирует host firewall | **ФАКТ:** публичная поверхность демо; production публиковать только через integration proxy/firewall |
| admin-console | 8090/TCP | `0.0.0.0:8090` | да, если не блокирует firewall | **ФАКТ:** direct HTTP demo; production только reverse proxy HTTPS |
| ldap | 389/TCP | `127.0.0.1:3893` | нет | demo/debug only; внутри bridge доступен всем контейнерам |
| postgres primary | 5432/TCP | `127.0.0.1:${POSTGRES_PORT:-5433}` | нет | локальная отладка; приложение использует service DNS |
| postgres replica | 5432/TCP | не опубликован | нет | bridge only |
| demo-runner | 8091/TCP | не опубликован | нет | доступен admin-console внутри bridge |
| Redpanda | 9092 internal, 19092 external listener | `127.0.0.1:19092` | нет | internal client использует `redpanda:9092`; нет auth |
| ClickHouse | 9000 native, 8123 HTTP | `127.0.0.1:9010`, `127.0.0.1:8123` | нет | admin/worker используют native 9000 |
| MinIO | 9000 S3, 9001 console | `127.0.0.1:9002`, `127.0.0.1:9003` | нет | console не должна публиковаться пользователям production |
| Ollama | вне Compose, 11434/TCP | определяется конфигурацией хоста | не следует из репозитория | доступ контейнеров обеспечен host-gateway; iptables-изоляция описана, но не закодирована здесь |
| delivery-trueconf | нет собственного server listener | весь контейнер использует host namespace | зависит от кода/SDK | повышенная связность; Docker bridge DNS недоступен |

**Критическая оговорка TrueConf host networking.** `delivery-trueconf` запускается с `network_mode: host`, чтобы подключаться к локальному TrueConf Server через `127.0.0.1:4309` и не добавлять адрес контейнера в trusted zone `tc_bridge`. Однако host-network контейнер не является участником Compose bridge и не разрешает имя `postgres`. Значение по умолчанию `postgresql+psycopg2://alert:alert@postgres:5432/alert_platform` для него непригодно, если не создана отдельная внешняя DNS/route-схема. На текущем однохостовом стенде нужно явно задавать `DELIVERY_DATABASE_URL=...@127.0.0.1:${POSTGRES_PORT:-5433}/alert_platform`; в production — использовать утверждённый HA DNS/VIP PostgreSQL. Это должно быть отдельным smoke-test после каждого deploy. Host mode также обходит Docker network isolation: любые будущие listener в адаптере окажутся прямо в namespace хоста.

## 7. Полная матрица фактических и планируемых потоков

В колонке «DNS/path» указано имя назначения и, для HTTP, путь. «TLS/auth» разделяет фактическую защиту и целевую. Порты, которых нет в коде/Compose, отмечены как целевые, а не реализованные.

| ID | Инициатор → получатель; направление | Протокол/порт; DNS/path | TLS и аутентификация | Данные и назначение | Отказ, retry и логирование |
|---|---|---|---|---|---|
| F01 | Zabbix → gateway; inbound | **ФАКТ:** HTTP POST, container 8080/host 8081; `/api/v1/ingest/raw` | token `X-Source-Token` обязателен только если token задан записи; compare constant-time. **ЦЕЛЕВОЕ:** HTTPS 443 + mTLS/HMAC + allow-list | JSON: `source_system`, `source_instance`, `raw_body`, optional `external_id`; raw body до 1 МиБ; запись WORM/queue | 202 после транзакции; duplicate безопасен по hash; 400/401/422/503 повторять по политике; логировать source, request-id, status, latency, hash, не token/body |
| F02 | SolarWinds → gateway; inbound | как F01 | как F01 | plain-text Alert/Reset внутри JSON-конверта; parser определяется `solarwinds.yaml` | как F01; schema drift/parse failure должны наблюдаться отдельно |
| F03 | новый источник → gateway; inbound | **ФАКТ:** только тот же HTTP API. **ЦЕЛЕВОЕ:** адаптеры syslog 6514, SNMP trap 162/UDP, AMQP 5671, SMTP relay 25/587, API polling 443 — после отдельной реализации | отдельная machine identity на каждый instance | сырой неизменённый сигнал | не открывать перечисленные порты заранее: текущий Go gateway их не слушает |
| F04 | gateway → PostgreSQL primary; outbound | PostgreSQL 5432/TCP; `postgres:5432/alert_platform` | **ФАКТ:** password URI, без заданного TLS в Compose. **ЦЕЛЕВОЕ:** TLS verify-full, SCRAM, отдельная роль gateway | `signals` WORM + `signal_queue` атомарно | DB failure → 503, источник повторяет; pg logs + app error/latency; не логировать URI |
| F05 | pipeline-worker → PostgreSQL; outbound/bidirectional session | 5432; `postgres` | demo общий user `alert`; target TLS/SCRAM и роль pipeline | claim через `FOR UPDATE SKIP LOCKED`, events/problems/incidents/state | stuck sweep 120s, poll 1s; errors stdout, queue status/error; алерт по age/depth |
| F06 | pipeline-worker → Ollama; outbound | HTTP 11434; `host.docker.internal`; `/api/chat`, `/api/embeddings` | **ФАКТ:** plain HTTP, без auth. **ЦЕЛЕВОЕ:** изолированная AI-zone, TLS/mTLS/API policy | текст алерта и локальный контекст; semantic normalization/dedup | best-effort timeout, `nil` fallback; не блокирует pipeline; логировать latency/status/model без полного prompt |
| F07 | delivery-planner → PostgreSQL; outbound | 5432; `postgres` | demo общий user; target role planner | читает problems/subscriptions/scenarios, пишет notifications и transactional outbox | poll 5s; ошибка не удаляет pending; stdout + outbox age/failure metrics |
| F08 | delivery-planner → Ollama; outbound | HTTP 11434; `host.docker.internal`; chat/embeddings | как F06 | summary, hypothesis, recommendation, RAG embedding | best-effort; default timeout 90s; доставка должна продолжиться без AI |
| F09 | delivery-trueconf → PostgreSQL; outbound из host namespace | 5432 DB protocol; **обязательно явно заданный address**, для demo loopback host 5433 | password URI; target TLS/SCRAM, отдельная role | claim `delivery_outbox`, notification status, incoming ACK/AI requests | retry/backoff до 8, stuck 120s, `SKIP LOCKED`; provider-send/DB-commit ambiguity терминально помечается; stdout без credential |
| F10 | delivery-trueconf → TrueConf Server; outbound, ответы в established session | Chatbot Connector API/WebSocket; code default 4309; `TRUECONF_SERVER`; adapter задуман на `127.0.0.1` | bot username/password получают token; HTTP/HTTPS через `TRUECONF_HTTPS`; **ЦЕЛЕВОЕ:** HTTPS/WSS, verify CA, least-privilege bot | готовый HTML-текст, recipient/chat ID, reply message ID; входящие команды и health | SDK reconnect/health; outbox retry; логировать command ID, provider message ID, attempts, connection state; никогда password/token |
| F11 | TrueConf Server ↔ сотрудник; корпоративный канал | vendor protocols/ports — **ТРЕБУЕТ СОГЛАСОВАНИЯ** | TrueConf identity/policy | уведомления, команды `/старт`, `/кабинет`, `/алерты`, `/сводка`, reply ACK/«анализ» | availability TrueConf определяет конечную доставку; клиентские порты не открываются к платформе |
| F12 | browser → admin-console; inbound | **ФАКТ demo:** HTTP host 8090. **ЦЕЛЕВОЕ:** HTTPS 443 через `platform.<corp>`/`/console`; SPA и `/api/*` | signed HttpOnly SameSite=Lax cookie; Secure зависит env и demo default false; LDAP login. Target TLS/HSTS/CSP/CSRF/MFA policy | UI, incidents, equipment, employees, sources, audit, metrics | 401/403/429/5xx; access/security audit, request-id; не логировать cookie/password/token query |
| F13 | browser/TrueConf link → personal cabinet; inbound | HTTPS target 443; `/me/{user}/?token=...`, `/alerts/{user}/?token=...` | **ФАКТ:** random access token + constant-time compare; **ЦЕЛЕВОЕ:** short-lived/one-time signed token, no query logging/referrer leakage | персональные subscriptions/current alerts | invalid token denied; audit access; redact URL query everywhere |
| F14 | admin-console → LDAP/AD; outbound | **ФАКТ demo:** LDAP 389 `ldap`; **ЦЕЛЕВОЕ:** LDAPS 636 or StartTLS | service bind searches escaped username, then user DN bind validates password; target CA verify | DN, `memberOf`, login result | 5s dial/search limits; auth fails closed; audit login failure without password |
| F15 | deprovision-worker → LDAP/AD; outbound | LDAP 389 `ldap`, poll 30s | service search credential; target LDAPS/read-only account | список/статус сотрудников для отключения подписок | directory outage must not stop alert pipeline; retry next cycle; audit deactivation/error |
| F16 | admin-console/deprovision → PostgreSQL; outbound | 5432 `postgres` | demo common user; target component roles | sessions не хранят пароль; admin data, audit, deprovision | API 503 or worker retry; DB/app logs and mutation audit |
| F17 | admin-console → demo-runner; internal outbound | HTTP 8091; `demo-runner`; selected `/api` demo/self-test paths through reverse proxy | **ФАКТ:** no service auth/TLS inside bridge | synthetic datagen/AI self-test only | admin returns 502; production отключить или вынести в отдельный non-prod profile |
| F18 | demo-runner → PostgreSQL/Ollama; internal outbound | DB 5432 `postgres`; HTTP 11434 host gateway | demo credentials; no TLS/auth to Ollama | synthetic dataset and self-tests | не влияет на production path; отдельные demo logs |
| F19 | PostgreSQL replica → primary; connection initiation; WAL возвращается primary→replica | PostgreSQL replication 5432; `postgres`; `pg_basebackup -R`, streaming replication | **ФАКТ:** role replicator, SCRAM, `pg_hba` allow docker subnet по SECURITY; **ЦЕЛЕВОЕ:** TLS, host-specific allow-list, secret vault | полный base backup и WAL, то есть все данные БД | init retries 5s; health; monitor replay lag/WAL; promotion ручной, automatic failover отсутствует |
| F20 | migrate → PostgreSQL; one-shot outbound | 5432 `postgres` | demo common owner; target dedicated migration role, time-limited access | sequential SQL schema migrations | blocks dependent startup on failure; retain migration logs and schema version |
| F21 | kb-index → PostgreSQL; one-shot outbound | 5432 `postgres` | target least privilege | knowledge markdown and embeddings in pgvector | manual run; idempotency/metrics; failure не влияет на existing runtime |
| F22 | kb-index → Ollama; one-shot outbound | HTTP 11434 host gateway `/api/embeddings` | target AI-zone policy | knowledge-base chunks, embedding model `nomic-embed-text` | retry/re-run; no external LLM |
| F23 | changelog-worker → PostgreSQL; outbound | 5432 `postgres` | target read/relay role | reads unsynced `change_events`, reads Signal batches, updates sync/watermark | `SKIP LOCKED`, retry; downstream outage cannot block gateway |
| F24 | changelog-worker → Redpanda; outbound producer | Kafka 9092; `redpanda`; topic `change_events.v1` | **ФАКТ:** no auth/TLS. **ЦЕЛЕВОЕ:** TLS/SASL, ACL topic-only | change event JSON with before/after, actor, result | at-least-once; synced_at only after produce; log lag/error, not sensitive before/after by default |
| F25 | changelog-worker ← Redpanda; consumer initiated by worker | Kafka 9092; consumer group `changelog-clickhouse` | как F24 | same change events | manual commit after ClickHouse insert; retry; ReplacingMergeTree dedup by event_id |
| F26 | changelog-worker → ClickHouse; outbound | native 9000; `clickhouse` | **ФАКТ:** password env, no declared TLS; target TLS and insert-only user | batch insert analytical history | failure retains Kafka offset; log rejected batch and lag; critical path unaffected |
| F27 | admin-console → ClickHouse; outbound | native 9000; `clickhouse` | demo same user; target read-only user/TLS | whitelisted low-code history search | unavailable → only search 503; values are parameters; audit query metadata |
| F28 | clickhouse-migrate → ClickHouse; one-shot | native 9000; `clickhouse` | target migration account | analytical DDL | Compose blocks admin/changelog dependency on migration completion; keep logs |
| F29 | changelog-worker → MinIO; outbound | **ФАКТ:** S3 HTTP 9000 `minio`; bucket `dispatcher-datalake`. **ЦЕЛЕВОЕ:** S3 HTTPS 443 | static root-like demo keys; target write-only service account + bucket policy/TLS | batches `raw/YYYY/MM/DD/from-to.ndjson` containing raw Signal | watermark advances after PutObject; retry same range; monitor age/object checks; gateway unaffected |
| F30 | operator browser → MinIO console; management | demo loopback host 9003→9001 only | demo root credentials; target bastion/MFA/admin account or disable console | bucket administration, not end-user function | audit admin actions; no publication from user/Internet zone |
| F31 | operator tools → loopback debug ports; local only | PostgreSQL 5433, LDAP 3893, Kafka 19092, ClickHouse 8123/9010, MinIO 9002/9003 | varies; loopback is network restriction, not identity | diagnosis/migrations/manual checks | production удалить host publication or restrict bastion; audit shell access |
| F32 | platform → monitoring/SIEM; outbound target | HTTPS 443, syslog TLS 6514, OTLP 4317/4318 or approved agent | mTLS/service identity | app/security logs, audit, metrics, traces, health — no bodies/secrets | local buffer; alert on delivery gap; **в Compose отсутствует** |
| F33 | backup agent → PostgreSQL; outbound/admin | pg_dump protocol 5432; repository SFTP/HTTPS/object API per standard | backup account, TLS, encrypted artifact, vault key | full logical backup; target WAL archive/PITR | **ФАКТ docs:** daily 03:00, 7-day rotation and restore test. Repository/encryption not verified; monitor backup age/result |
| F34 | all nodes/services → DNS | UDP/TCP 53 to corporate resolvers; Docker embedded DNS internally | restrict recursion, DNSSEC where policy supports | service/host names | cache/redundant resolvers; log anomalies, not every query indefinitely; production values unknown |
| F35 | all nodes/services → NTP | UDP 123 to zone-local NTP relay | authenticated NTP/NTS where supported | timestamps for dedup, SLA, correlation, certificates | alert on offset/stratum/source; direct internet NTP forbidden |
| F36 | services/reverse proxy → PKI | enrollment/revocation via approved CA protocols; CRL/OCSP HTTP/HTTPS | machine identity, dual control for CA admin | certificates and revocation status | fail policy agreed by service; expiry alerts 30/14/7 days; endpoints unknown |
| F37 | build/update host → internal mirrors/proxy | HTTPS 443 | proxy auth, repository signing, allow-list | base images, PyPI requirements, Go modules, npm packages, OS packages | builds fail closed on signature/hash policy; runtime egress not needed; external URLs present only as upstream defaults |
| F38 | management workstation → host/orchestrator | SSH 22 or corporate management protocol — target only | bastion, MFA, named account, JIT privilege | deploy, diagnostics, controlled promotion/restore | session recording/audit; not described in Compose; direct Internet SSH prohibited |

## 8. Целевая production-топология и зоны

**ЦЕЛЕВОЕ.** Минимально рекомендуются следующие раздельные зоны. Конкретные VLAN/VRF и адреса требуют согласования.

1. **OT/source zones.** Zabbix/SolarWinds/коллекторы находятся рядом со своими наблюдаемыми системами. Им разрешён только исходящий поток к паре relay в IDMZ. Платформа не инициирует соединения к PLC/HMI/SCADA. Для изолированной АСУ ТП применяется схема из отдельного документа `03-asutp-isolated-flow-model.md`.
2. **Integration DMZ.** Два ingress relay/API gateway завершают source TLS, валидируют client certificate/token, размер и rate, сохраняют локальный spool и передают событие далее по новой TLS-сессии. Зона не маршрутизирует произвольный трафик между OT и IT.
3. **User access DMZ.** Reverse proxy/WAF публикует только HTTPS UI/API. Source ingest и пользовательский UI лучше разделить разными virtual host, сертификатами, rate policies и access logs.
4. **Application zone.** Gateway, pipeline, planner, admin API, deprovision и TrueConf adapter. У каждого процесса своя identity и минимальный сетевой policy. Demo-runner отсутствует.
5. **Data zone.** PostgreSQL primary/replica, ClickHouse, Redpanda, MinIO. Прямой доступ из пользовательской зоны запрещён. Management console MinIO не публикуется.
6. **AI zone.** Отдельный Ollama/GPU node. Доступен только pipeline/planner/admin по фиксированному endpoint; internet egress отсутствует. Prompts/answers не уходят в сторонние API.
7. **Corporate services zone.** AD/LDAP, TrueConf, DNS, NTP, PKI. Разрешаются только точечные клиентские потоки приложения.
8. **Management/observability/backup zones.** Bastion, registry mirrors, SIEM, monitoring и immutable backup отделены от runtime и друг от друга. Production-оператор не подключается напрямую к Docker socket/DB с пользовательской рабочей станции.

Для HA application-сервисы размещаются минимум на двух узлах/в оркестраторе с anti-affinity. Gateway и admin находятся за разными virtual service/load balancer. PostgreSQL получает утверждённый failover manager/VIP или DNS с низким TTL; текущая replica без автоматического promotion этого не обеспечивает. Redpanda/ClickHouse/MinIO проектируются по требуемым RPO/RTO: текущие одиночные узлы — только demo.

## 9. Минимальная firewall matrix

Ниже — минимальный набор allow-правил production. Политика для всех зон: stateful deny-by-default; обратные пакеты разрешаются только в established/related session. Любое правило содержит owner, заявку, срок пересмотра и логирование deny.

| № | Source zone/object | Destination | Service | Action | Обоснование |
|---|---|---|---|---|---|
| FW01 | зарегистрированные source IP / OT relay | ingress relay VIP | TCP 443 | allow | единственный приём событий, mTLS/token |
| FW02 | ingress relay service identity | gateway VIP | TCP 443 | allow | повторная передача проверенного события |
| FW03 | user/admin subnets | user reverse proxy VIP | TCP 443 | allow | SPA/API; admin path дополнительно по group/VPN |
| FW04 | gateway app identity | PostgreSQL primary VIP | TCP 5432 | allow | WORM + queue |
| FW05 | pipeline/planner/admin/deprovision/TrueConf identities | PostgreSQL primary VIP | TCP 5432 | allow per identity | только необходимые SQL-роли |
| FW06 | replica addresses | PostgreSQL primary | TCP 5432 replication | allow | basebackup/WAL; host-specific, не вся container subnet |
| FW07 | pipeline/planner/admin/kb-index identities | Ollama VIP | TCP 443 target | allow | локальный AI API через TLS gateway |
| FW08 | admin/deprovision identities | LDAP/AD VIP | TCP 636 | allow | LDAPS bind/search |
| FW09 | TrueConf adapter identity | TrueConf connector VIP | TCP 4309 либо утверждённый vendor port | allow | bot session; TLS обязателен |
| FW10 | admin-console | demo-runner | none in production | deny | demo component удалён |
| FW11 | changelog-worker | Redpanda brokers | TCP 9093 target TLS | allow | topic change_events only by ACL |
| FW12 | changelog-worker/admin | ClickHouse VIP | TCP 9440 target TLS/native | allow | write/read разными users |
| FW13 | changelog-worker | MinIO S3 VIP | TCP 443 | allow | raw archive, bucket-scoped identity |
| FW14 | runtime nodes | approved DNS/NTP/PKI/SIEM | 53 TCP/UDP; 123 UDP; 443/6514 as approved | allow | platform services; точные адреса |
| FW15 | backup agents | DB/data + backup repository | exact backup protocols | allow on schedule/policy | backup/restore; separate identity |
| FW16 | bastion | management interfaces | SSH 22/API as approved | allow JIT | named admin and session recording |
| FW17 | any user/Internet/OT | PostgreSQL, LDAP, Ollama, Kafka, ClickHouse, MinIO, container debug ports | any | deny + alert | запрет обхода application layer |
| FW18 | application/DMZ/OT runtime | Internet | any | deny | runtime не требует внешнего облака/репозитория |

**ТРЕБУЕТ СОГЛАСОВАНИЯ.** TrueConf может использовать иной порт/набор endpoints в зависимости от версии и конфигурации Server. До выпуска FW09 администратор TrueConf предоставляет документированный connector endpoint, transport, certificate chain и source address adapter.

## 10. Ingress- и egress-политика

### 10.1. Ingress

Разрешены только два логических ingress-класса: machine-to-machine ingest и пользовательский HTTPS. Для source ingest должны быть отдельный FQDN, client certificate и token на каждый `source_instance`; неизвестный instance отклоняется. Это строже текущей обратной совместимости gateway: сейчас запись источника без `api_token` принимается без token. Перед production все записи мигрируются на обязательный token, затем nullable-режим удаляется. На edge применяются max body 1 МиБ или меньше по паспорту, timeouts, connection/rate limits, content type, запрет неожиданных methods/paths и request ID.

Пользовательский ingress публикует SPA и только необходимые API. `/docs` и `/openapi.json` либо защищаются той же auth, либо отключаются. `/api/auth/login` имеет application limiter (пять ошибок за пять минут на login), но edge должен добавить IP/device-aware rate limit. `/me` и `/alerts` с token query требуют запрета записи query string в proxy/browser analytics/referrer, короткого TTL и ротации. Admin endpoints разрешаются только LDAP-группам и, при корпоративном требовании, только через VPN/PAW.

Никакие DB/Kafka/S3/LDAP/Ollama/debug endpoints не публикуются. Фактические `8081:8080` и `8090:8090` заменяются bind на private interface или вообще отсутствуют за service mesh/ingress. Loopback host mappings остаются только на контролируемом bastion host либо удаляются.

### 10.2. Egress

Runtime-процессы не требуют доступа в Интернет. Разрешается только service-to-service трафик по F04–F36. AI-вызовы идут к локальному Ollama; запрет внешних LLM API контролируется DNS/firewall/proxy, а не только договорённостью разработчиков. TrueConf adapter может соединяться только с утверждённым TrueConf endpoint; LDAP-клиенты — только с directory VIP; source gateway не должен опрашивать OT в текущем webhook-сценарии.

Сборка и обновление выполняются отдельными build nodes через внутренние mirrors. Если временно нужен корпоративный egress proxy, allow-list задаётся по repository host, TLS inspection согласуется с проверкой подписей, а правило имеет срок. Передача production raw data на build node запрещена. Telemetry SDK, CDN и внешние шрифты в runtime не допускаются; gateway docs уже встроены без внешнего CDN.

## 11. DNS, время, PKI, proxy и обновления

### DNS

**ФАКТ.** В Compose внутренние имена `postgres`, `ldap`, `redpanda`, `clickhouse`, `minio`, `demo-runner` разрешает embedded Docker DNS. `host.docker.internal` добавляется через `host-gateway`. Host-network TrueConf adapter этой DNS-зоной не пользуется. **ЦЕЛЕВОЕ:** production FQDN выдаются корпоративным DNS, раздельно для source ingress, user UI, PostgreSQL HA, TrueConf connector, LDAP, Ollama, S3/Kafka/ClickHouse. Не следует привязывать сертификаты и firewall к эфемерным container IP. Для HA заранее определяются TTL, health-based failover и поведение клиентов при кэшировании. Прямой DNS в Интернет запрещён.

### NTP

Корреляция, dedup, SLA, token expiry, TLS и аудит зависят от согласованных часов. Все host/VM/container используют host clock, а hosts синхронизируются с двумя корпоративными NTP-реле. OT использует своё реле и, при разрешении, иерархически сверяется через IDMZ. Контроль: offset, jitter, stratum, смена источника и потеря синхронизации; порог аварии задаёт ИБ/эксплуатация. В событии сохраняются `occurred_at` источника и `received_at` платформы, чтобы дрейф не искажал порядок бесследно.

### PKI

Нужны отдельные server certificates для ingress/UI/data endpoints и client certificates для source instances/service identities. Private keys находятся в vault/HSM или orchestrator secrets, не в environment dump и не в image. Сертификаты содержат только утверждённые SAN; chain и revocation проверяются; auto-renew сопровождается expiry-алертами. Решение о ГОСТ TLS/VPN и сертифицированных СКЗИ — **ТРЕБУЕТ СОГЛАСОВАНИЯ**. Для внутреннего трафика нельзя отключать verify ради self-signed certificate: CA добавляется в trust store образа.

### Proxy и updates

Dockerfile показывает build-time обращения к Alpine/Debian repositories, PyPI, Go modules и npm registry; base images и готовые сервисные images также загружаются из registry. Production pipeline должен использовать внутренний registry/proxy с pinning по digest, malware/vulnerability scan, SBOM, license policy и provenance. Обновления переносятся в закрытый контур после подписи/хэш-проверки и staged testing. Runtime environment получает immutable images; `latest` для glauth и непинованные зависимости запрещаются. Emergency update проходит change window и rollback plan. OT update flow отделяется от event flow и не создаёт обратного административного канала.

## 12. Наблюдаемость, аудит и резервное копирование

**ФАКТ.** Gateway имеет `/api/v1/health`; Admin Console предоставляет защищённый `/api/metrics`/summary; Compose healthchecks есть у PostgreSQL, replica, Redpanda, ClickHouse и MinIO. Приложения пишут ошибки и lifecycle в stdout. Admin API ведёт audit для login failures и административных изменений. Outbox/queue сохраняют status/error, что обеспечивает предметную трассировку. Централизованный Prometheus/OTel/log collector/SIEM в Compose отсутствует.

**ЦЕЛЕВОЕ.** Monitoring должен покрывать: availability каждого listener; p95/p99 ingest latency; HTTP code rate; queue depth и oldest age; parse failure/schema drift; CI resolution; incidents; outbox pending/failed/oldest; TrueConf session health и end-to-end synthetic delivery; PostgreSQL connections/locks/disk/WAL/replication lag; Kafka consumer lag; ClickHouse insert errors; MinIO archive watermark/age; Ollama latency/timeouts/model loaded/VRAM; LDAP latency/failure; certificate expiry; NTP offset; deny events и неожиданные новые listeners. Тело алерта, credentials, cookie, token query и LLM prompt не попадают в обычные access/application logs. Доступ к audit/read logs разделяется с правом изменения конфигурации.

**ФАКТ из `INFRASTRUCTURE.md`.** На демонстрационном сервере описан ежедневный `pg_dump` в 03:00 с ротацией 7 дней и проверенным пробным восстановлением, а также streaming replica с измеренным малым lag. Скрипты и место хранения находятся вне этого репозитория, автоматического failover нет.

**ЦЕЛЕВОЕ.** Утверждаются RPO/RTO отдельно для принятых Signal, pending delivery, конфигурации, audit и downstream analytics. Для PostgreSQL нужны encrypted full backup + WAL archive/PITR, off-host/immutable copy и регулярный restore drill. Backup должен включать миграции/config, parser/rules, MinIO objects и, если бизнесу нужна аналитическая история, Redpanda/ClickHouse либо воспроизводимый rehydrate. Ключи backup хранятся отдельно. Runbook promotion защищает от split brain, меняет DB endpoint, проверяет очереди/outbox и не допускает повторной отправки уже доставленных сообщений.

## 13. Отказы и ожидаемая деградация

Если edge/gateway недоступен, источник не получает `202`, сохраняет событие в spool и повторяет с тем же ID/hash. Отказ primary останавливает ingest и зависимые процессы: replica не повышается автоматически, поэтому применяется runbook promotion со split-brain и queue/outbox checks. При падении pipeline Signal остаются в queue, stuck sweep возвращает зависшие записи, а scale-out безопасен через `SKIP LOCKED`.

Отказ Ollama только убирает AI-дополнение. Отказ planner задерживает создание outbox; отказ TrueConf/adapter накапливает retry/backoff с видимым status/error. LDAP outage закрывает новые login и задерживает deprovision, но не pipeline. Redpanda/ClickHouse/MinIO могут отстать или дать 503 только в downstream-функции; после возврата worker продолжает по offset/watermark. DNS/NTP/PKI outage рассматривается как общий инфраструктурный инцидент: запрещено обходить его отключением TLS verify или ручной подменой времени.

## 14. Приёмочный checklist

### Сеть и публикация

- [ ] Согласованы реальные схемы L2/L3, VLAN/VRF, CIDR, VIP/FQDN и владельцы всех правил.
- [ ] Снаружи доступны только source HTTPS и user HTTPS; scan подтверждает deny для 389/5432/8080/8090/8091/9000/9001/9092/11434 и debug mappings.
- [ ] Все правила реализуют exact source/destination, deny-by-default, owner, срок ревизии и журналирование.
- [ ] OT/источник инициирует соединение наружу; отсутствует маршрут application/user/TrueConf → OT devices.
- [ ] Demo-runner и demo credentials отсутствуют в production profile.
- [ ] Host-network caveat TrueConf закрыт: adapter достигает БД по утверждённому endpoint и TrueConf по точному локальному/VIP endpoint; Docker DNS не предполагается.

### TLS, identity и секреты

- [ ] Ingest/UI/LDAP/TrueConf/PostgreSQL/Kafka/ClickHouse/S3/AI защищены TLS там, где пересекают trust boundary; client/server verify протестирован.
- [ ] Все source instances имеют обязательный unique token и mTLS certificate; неизвестный или tokenless source получает отказ.
- [ ] Service accounts разделены по gateway, pipeline, planner, admin, delivery, changelog, backup и migration; SQL/Kafka/S3 privileges минимальны.
- [ ] Secrets отсутствуют в Git, image, Compose defaults и логах; rotation/revocation проверены.
- [ ] Session cookie Secure/HttpOnly/SameSite, HSTS/CSP/CSRF и login rate limiting подтверждены DAST/negative tests.
- [ ] Версия TrueConf, connector port, HTTPS/WSS, CA, bot rights, reconnect и reply semantics подтверждены на реальном сервере.

### Функциональные потоки и отказоустойчивость

- [ ] Zabbix и SolarWinds получают 202 только после атомарной записи Signal+queue; duplicate retry не создаёт второй Signal.
- [ ] Проверены 401/400/422/503, limit 1 МиБ, rate limit, parser failure и schema drift без потери оригинала.
- [ ] End-to-end trace связывает source request → Signal → Event/Problem/Incident → Notification/outbox → TrueConf message ID → reply ACK.
- [ ] Отключение Ollama не задерживает и не блокирует доставку.
- [ ] Отключение Redpanda, ClickHouse и MinIO не меняет результат ingest/delivery; после возврата lag сходит без потери.
- [ ] TrueConf disconnect создаёт контролируемый backlog и восстанавливается без неконтролируемых дублей.
- [ ] PostgreSQL failover/restore выполнен по runbook; проверены RPO/RTO, queue/outbox, reconciliation и отсутствие split brain.

### Эксплуатация

- [ ] DNS HA/TTL, NTP sources/offset thresholds, PKI issuance/revocation/expiry и proxy allow-list оформлены эксплуатационными регламентами.
- [ ] SIEM/monitoring получает health, queue/outbox age, TrueConf connection, replication/backup lag, TLS/NTP/firewall events; тела и секреты редактируются.
- [ ] Backup зашифрован, имеет immutable/off-host copy; restore drill подтверждён протоколом и охватывает schema/config/data.
- [ ] Build использует внутренние mirrors, pinned digests, SBOM, signature/provenance и vulnerability/license gates; runtime Internet egress запрещён.
- [ ] Сетевой документ актуализируется после каждого нового connector/channel/data service; неиспользуемое правило закрывается.

## 15. Что требует отдельного согласования или проверки

1. Реальные IP, подсети, VLAN/VRF, NAT, HA firewall/load balancer и DNS-имена отсутствуют в репозитории.
2. Не предоставлены production endpoint и сертификаты Zabbix/SolarWinds, точные source IP, политика повторов и возможность mTLS/HMAC.
3. Не подтверждены версия TrueConf Server, фактический connector port, transport HTTP/HTTPS/WSS, цепочка CA, required trusted zone и полный vendor port matrix. Код использует default 4309 и допускает `TRUECONF_HTTPS=false`.
4. Не проверена фактическая конфигурация внешнего reverse proxy для домена `газвпол.рус`: TLS versions/ciphers, HSTS/CSP, redirect, WAF, route mapping и отсутствие query-token в access logs.
5. Не известны production LDAP/AD DN, HA endpoints, LDAPS CA, права service account, MFA/SSO и политика жизни уже выданной session при directory outage.
6. Не зафиксированы корпоративные DNS/NTP/PKI/SIEM/proxy/registry/backup endpoints и требования ГОСТ/сертифицированных СЗИ.
7. Описанные на сервере iptables для Ollama/Open WebUI и backup scripts находятся вне репозитория; их persistence, owner, checksum, encryption и мониторинг требуют проверки на целевой машине.
8. Не утверждены RPO/RTO, automatic PostgreSQL failover, backup/PITR всех хранилищ, HA Redpanda/ClickHouse/MinIO и DR-площадка.
9. Не реализованы заявленные спецификацией syslog/SNMP/AMQP/SMTP/API-polling adapters; открывать их порты до реализации и threat review нельзя.
10. Не подтверждены production TLS/auth для внутренних PostgreSQL, Ollama, Redpanda, ClickHouse и MinIO; текущий Compose использует преимущественно plain protocols и demo credentials.

До закрытия этих пунктов схема пригодна как детальная основа проектирования и приёмки, но не как безусловное разрешение на ввод production-сетевых правил.
