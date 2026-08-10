# Платформа «Диспетчер». Лицензирование

**Статус документа:** проект лицензионного профиля по фактическому составу репозитория на 10.08.2026  
**Дата веб-проверки первичных источников:** 10.08.2026  
**Назначение:** определить, какие права и услуги действительно требуется приобрести или согласовать для пилота и промышленного контура, какие компоненты можно использовать по открытым лицензиям, какие обязательства возникают при поставке и сетевой эксплуатации, а также какие вопросы нельзя закрыть без правообладателя и юридической службы.

> **Юридическая оговорка.** Документ является инженерной инвентаризацией и рекомендацией по управлению лицензиями, но не юридическим заключением. Решение о допустимости AGPL, BSL, модельных лицензий, EULA отечественных дистрибутивов и способа распространения итогового комплекса должен письменно подтвердить юрист, специализирующийся на программном обеспечении и интеллектуальной собственности. Коммерческие предложения и лицензионные сертификаты должны проверяться применительно к конкретному юридическому лицу, территории, числу серверов, ядер, пользователей и сред.

## 1. Как читать выводы

Каждое существенное заключение имеет один из трёх статусов.

- **ПОДТВЕРЖДЕНО** — компонент и его версия или диапазон обнаружены в репозитории, а лицензия проверена по официальному файлу LICENSE, документации правообладателя или карточке выпуска владельца пакета.
- **ПРЕДВАРИТЕЛЬНО** — семейство лицензии известно, но точный артефакт не зафиксирован digest/хешем, версия разрешается плавающим диапазоном либо итог зависит от способа использования и распространения.
- **НЕ ОПРЕДЕЛЕНО** — в репозитории нет версии, лицензионного текста, договора, коммерческого предложения или иных данных, достаточных для вывода.

Слово «бесплатно» ниже означает отсутствие обязательного лицензионного платежа при соблюдении условий конкретной лицензии. Оно не означает отсутствие обязанностей, стоимости сопровождения, санкционного риска, уязвимостей, товарных знаков или расходов на эксплуатацию. Ни один компонент не считается «безрисковым» только из-за метки Open Source.

Проверены фактические файлы `docker-compose.yml`, корневой и Go/Admin Dockerfile, `go.mod`, `go.sum`, `requirements.txt`, `requirements-dev.txt`, `datagen/requirements.txt`, `pyproject.toml`, `web/package.json`, `web/package-lock.json`, конфигурация Ollama в Compose и Go-коде, `ALERT-PLATFORM-SPEC.md` (особенно 1.4 и 15), `ARCHITECTURE.md`, `GO-MIGRATION.md` и `INFRASTRUCTURE.md`.

## 2. Резюме: что покупать, что согласовывать, что не является лицензией

### 2.1. Обязательная или условная закупка

| Позиция | Решение | Основание и условие |
|---|---|---|
| **TrueConf Server** | **УСЛОВНО К ЗАКУПКЕ / ПОДТВЕРЖДЕНИЮ** | По ТЗ TrueConf уже развёрнут и является обязательным каналом. Новая лицензия не нужна, если действующий договор покрывает требуемый сервер, версию 5.5+, 87 пользователей, bot account и режим закрытого контура. Иначе приобретается подходящая редакция/поддержка. Нельзя считать наличие установленного сервера доказательством прав. |
| **Online/PRO users TrueConf** | **ПОДТВЕРДИТЬ ЁМКОСТЬ, затем при дефиците докупить** | Официальная модель считает online-подключения по устройствам, а PRO — по пользователям, участвующим в групповых конференциях. Для оповещений и чата нужны прежде всего достаточные online entitlements; термин «CAL» не является официальным названием TrueConf. Число покупаемых прав определяется фактической одновременностью устройств, а не просто числом 87 сотрудников. |
| **TrueConf Chatbot Connector / Chatbot API** | **ОТДЕЛЬНАЯ ЗАКУПКА НЕ ПОДТВЕРЖДЕНА** | Официальная документация указывает, что API включён в TrueConf Server начиная с 5.5.0, а библиотека поддерживает Server Free, Server и Enterprise. В смету нельзя автоматически добавлять отдельную лицензию Connector без письменного ответа TrueConf или коммерческого предложения. Нужно подтвердить право production-использования bot account и доступ к `tc_bridge:4309` в имеющейся редакции. |
| **MinIO commercial license или замена** | **ТРЕБУЕТСЯ РЕШЕНИЕ ДО PRODUCTION** | Серверный образ указан как `RELEASE.2025-04-08T15-41-24Z`; upstream относит community server к AGPL-3.0 и предлагает коммерческую лицензию. Если организация не готова выполнять AGPL и принимать сетевой copyleft-риск, MinIO следует заменить на согласованное S3-хранилище либо приобрести коммерческие права. |
| **Redpanda Enterprise** | **НЕ ПОКУПАТЬ при Community-only; купить только при enterprise-функциях** | Версия `v24.3.13` использует Community Edition под BSL; она допускает внутреннее применение с ограничениями, но не является OSI Open Source. Enterprise-функции требуют ключа. Начиная с 24.3 кластер получает trial enterprise features, поэтому эксплуатация должна доказать, что после trial не используются платные возможности. |
| **Postgres Pro / Astra Linux / Deckhouse** | **УСЛОВНО К ЗАКУПКЕ ДЛЯ ЦЕЛЕВОГО КОНТУРА** | Их нет в текущем Compose: стенд работает на PostgreSQL/pgvector и Docker Compose. Покупка обоснована только утверждённой production-архитектурой, требованиями реестра/ФСТЭК, SLA и вендорской поддержкой. Редакция и метрика лицензирования выбираются после sizing. |
| **Поддержка ClickHouse, Docker, Ollama и OSS** | **ОПЦИОНАЛЬНО** | Базовые найденные редакции допускают использование без лицензионного платежа, но организация может купить поддержку. Это услуга/коммерческий договор, а не техническая необходимость приложения. |

### 2.2. Что не покупать как программную лицензию

GPU, серверы, диски и сетевое оборудование — капитальное оборудование, а не лицензии платформы. Для Tesla T4 или иного ускорителя учитываются стоимость оборудования/аренды, гарантия и энергопотребление. Однако драйвер NVIDIA, CUDA Toolkit и контейнерный runtime имеют собственные соглашения: их версии и условия надо включить в SBOM инфраструктуры, особенно если образ с CUDA будет передаваться заказчику. Репозиторий не содержит CUDA Dockerfile и не фиксирует версию Ollama/драйвера, поэтому эта часть **НЕ ОПРЕДЕЛЕНА**.

Не требуется лицензировать каждый webhook Zabbix/SolarWinds как пользователя «Диспетчера». Права на сами Zabbix, SolarWinds и исходные системы находятся вне состава поставляемого приложения; интеграция по API не расширяет автоматически их лицензии. Но заказчик должен подтвердить, что его действующие договоры разрешают API-доступ и требуемое число polling/API connections.

## 3. Фактический состав поставки и проблемы воспроизводимости

### 3.1. Runtime-сервисы

Compose запускает собственные Go-бинарники `gateway`, `pipeline-worker`, `delivery-planner`, `admin-console`, `kb-index`, `clickhouse-migrate`, `changelog-worker`; Python-процессы миграции, deprovision, demo и TrueConf adapter; React SPA; PostgreSQL с pgvector; GLAUTH; Redpanda; ClickHouse; MinIO. Ollama работает на хосте и вызывается по HTTP, а TrueConf Server является внешним по отношению к репозиторию продуктом.

В контейнерных образах обнаружены следующие базы: `python:3.11-slim`, `golang:1.25-alpine`, `node:20-alpine`, `alpine:3.22`. Это не четыре «однолицензионных» артефакта: каждый образ содержит ОС-пакеты и библиотеки с собственными лицензиями. Например, Alpine распространяет пакетный состав под множеством лицензий, а Debian slim содержит пакеты Debian. Для передачи образов заказчику нужен отчёт по слоям финального image digest, а не только лицензия Python, Go или Node.js.

### 3.2. Зафиксированные и плавающие версии

**ПОДТВЕРЖДЕНО:** Redpanda закреплена на `v24.3.13`, MinIO — на выпуске `RELEASE.2025-04-08T15-41-24Z`, базовый Alpine runtime — на ветке `3.22`; Go modules и Node packages имеют lock-файлы с точными версиями.

**ПРЕДВАРИТЕЛЬНО:** `clickhouse/clickhouse-server:24.8`, `pgvector/pgvector:pg16`, `python:3.11-slim`, `golang:1.25-alpine`, `node:20-alpine` и `glauth/glauth:latest` не закреплены registry digest. Повторная сборка может получить другой набор файлов и лицензий под тем же тегом. `latest` для GLAUTH особенно неприемлем для доказательной ведомости.

**НЕ ОПРЕДЕЛЕНО:** Python runtime не имеет lock-файла. Требования вида `fastapi>=0.111`, `sqlalchemy>=2.0`, `python-trueconf-bot>=1.4.2` позволяют установку будущих релизов с изменившимися зависимостями и, теоретически, лицензиями. `requirements-dev.txt` добавляет `pytest>=7.4`; `datagen` повторяет PyYAML. До промышленной поставки следует сформировать hash-locked requirements через `pip-compile --generate-hashes` или эквивалент.

### 3.3. Код самой платформы

В корне `alert-platform` отдельный файл LICENSE для собственного кода не обнаружен. Следовательно, право заказчика устанавливать, копировать, модифицировать и передавать саму платформу **НЕ ОПРЕДЕЛЕНО** репозиторием. Авторское право возникает независимо от наличия LICENSE; отсутствие файла не превращает код в свободный. До поставки нужен выбранный режим: служебное произведение и передача исключительных прав, простая лицензия по договору либо внутренняя собственность заказчика. В LICENSE/NOTICE проекта также следует перечислить third-party notices.

## 4. TrueConf: точная граница лицензирования

### 4.1. Сервер и пользователи

Официальное [руководство TrueConf Server по лицензированию](https://trueconf.com/docs/server/en/admin/license/) разделяет extensions и типы подключений. Online user — авторизованное подключение к серверу; лицензия связана с устройством, поэтому один сотрудник на ПК и телефоне может занимать две online-лицензии. PRO user привязан к TrueConf ID и нужен для участия в групповых конференциях. Guest и SIP/H.323/RTSP connections считаются отдельно. В Enterprise различие между PRO и online устранено по правилам этой редакции.

Для «Диспетчера» бот создаёт личные чаты, отправляет сообщения и принимает ответы. Само приложение не организует видеоконференции, гостей и SIP-подключения. Поэтому предварительный sizing — 87 сотрудников с коэффициентом одновременных устройств плюс техническая учётная запись бота; PRO-права нужны только тем пользователям, чьи обязанности вне платформы требуют групповых конференций. Точный способ учёта bot connection в действующем релизе должен письменно подтвердить TrueConf.

[Калькулятор TrueConf Server](https://trueconf.com/server/buy/) показывает редакции, срок annual/lifetime, пакеты online users и поддержку, но веб-цена не заменяет коммерческое предложение для российского юрлица. Закупка должна содержать наименование редакции, версию, количество online/PRO/guest/gateway rights, право виртуализации и резервного экземпляра, срок обновлений, уровень поддержки, возможность offline activation и реквизиты правообладателя.

TrueConf Server Free нельзя автоматически считать вариантом для закрытого промышленного контура. Официальное руководство требует от Free постоянного соединения с registration server по TCP 4310 для проверки лицензии. Это конфликтует с политикой полностью изолированного периметра, если исключение на исходящий трафик не согласовано. Платная/offline схема должна быть подтверждена договором и инструкцией конкретной версии.

### 4.2. Chatbot API и Python SDK

Официальная [документация Chatbot Connector](https://trueconf.com/docs/chatbot-connector/en/overview/) говорит, что TrueConf Server API for Chat Bots включён с версии 5.5.0. Официальный репозиторий [`python-trueconf-bot`](https://github.com/TrueConf/python-trueconf-bot) указывает поддержку TrueConf Server 5.5+, Enterprise и Server Free. Значит, по открытым данным отдельный коммерческий модуль Connector для существующей версии **не подтверждён**.

При этом перед запуском production нужны письмо вендора или строка коммерческого предложения, подтверждающие: разрешение bot API в купленной редакции; число bot accounts/connections; поддержку личных и групповых чатов; отсутствие отдельной серверной лицензии; допустимость локального `tc_bridge` и резервного экземпляра. Если вендор даст противоположное условие, договор имеет приоритет над общей веб-документацией.

Библиотека `python-trueconf-bot` версии 1.4.2 опубликована 03.08.2026 в [официальной карточке PyPI](https://pypi.org/project/python-trueconf-bot/) с provenance на репозиторий TrueConf и выражением `BSD-3-Clause-Clear`. Это разрешительная лицензия: отдельная закупка SDK не нужна, но при распространении надо сохранять copyright, текст лицензии и disclaimer. SDK не заменяет права на проприетарный сервер/API.

## 5. Разрешительные OSS-компоненты и обязанности

### 5.1. Базы, аналитика и служебные сервисы

PostgreSQL 16 в образе pgvector использует разрешительную [PostgreSQL License](https://www.postgresql.org/about/licence/); [pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE) использует ту же лицензию. Коммерческое внутреннее применение, модификация и распространение разрешены без платы при сохранении copyright и текста условий. Нет гарантии и обязательной поддержки. Образ `pgvector/pgvector:pg16` всё равно нужно закрепить digest и просканировать: в нём есть PostgreSQL, pgvector и ОС-пакеты.

ClickHouse server 24.8 и Go driver относятся к Apache-2.0; первичный источник — [LICENSE ClickHouse](https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE). Apache-2.0 разрешает коммерческое использование и распространение, требует передать текст лицензии, сохранить notices, отметить изменённые файлы и учесть NOTICE; содержит патентную лицензию с условием прекращения при определённом patent litigation. Товарный знак ClickHouse лицензией не передаётся.

GLAUTH имеет [MIT License](https://github.com/glauth/glauth/blob/master/LICENSE), но Compose использует `latest`. Платёж не требуется, copyright/license должны сохраниться. GLAUTH в проекте — синтетический LDAP стенда; в production его предполагается заменить корпоративным AD/LDAP, лицензирование которого является внешней зависимостью заказчика.

### 5.2. Языки, серверные библиотеки и frontend

Go toolchain распространяется по BSD-подобной лицензии, Python — по PSF License Agreement и сопутствующим историческим лицензиям, Node.js — по MIT для собственного кода и набору лицензий bundled dependencies. Использование компиляторов не навязывает их лицензию собственному коду. Но файлы из стандартных библиотек, попавшие в бинарник/образ, должны учитываться scanner'ом.

Прямые Go-зависимости: `pgx/v5 v5.7.6`, `go-ldap/ldap/v3 v3.4.12`, `yaml.v3 v3.0.1`; для побочного контура — `clickhouse-go/v2 v2.48.0`, `franz-go v1.21.5`, `minio-go/v7 v7.2.1`. Их заявленные лицензии преимущественно MIT/BSD/Apache-2.0. В `go.mod` присутствуют также Azure NTLM, Brotli, xxhash, compress, OpenTelemetry, `golang.org/x/*` и другие транзитивные модули. Коммерческое применение предварительно допустимо, но финальный бинарник должен получить автоматическую лицензионную ведомость по `go list -m all` и исходным LICENSE на зафиксированных версиях.

Frontend lock фиксирует React 18.3.1, React DOM 18.3.1, React Router DOM 6.30.4, React Flow 11.11.4, TanStack Query 5.101.4, Vite 5.4.21, TypeScript 5.9.3, Tailwind CSS 3.4.19 и транзитивные пакеты. Основная масса имеет MIT; TypeScript и часть tooling — Apache-2.0; встречаются ISC, BSD-3-Clause, CC-BY-4.0 для данных `caniuse-lite`. Это разрешительные условия, но production bundle может включать copyright/assets и sourcemaps. Нужны `THIRD_PARTY_NOTICES` и результат `license-checker`/ORT/ScanCode, а dev-only пакеты следует отделить от реально поставляемых файлов.

Прямые Python requirements: FastAPI, Uvicorn, SQLAlchemy, psycopg2-binary, Pydantic, PyYAML, `python-trueconf-bot`, python-multipart, ldap3, itsdangerous и зависимости datagen. Большинство — MIT/BSD/Apache, но `ldap3` заявляет LGPL-3.0-or-later, а `psycopg2` использует LGPL с исключением. Эти лицензии обычно допускают коммерческое динамическое использование, но требуют сохранить условия и не запрещают обязанности при модификации самой библиотеки. Из-за отсутствия lock точный список и версии **НЕ ОПРЕДЕЛЕНЫ**.

Практические обязанности для permissive OSS:

1. не удалять copyright, LICENSE и NOTICE из исходных и бинарных поставок;
2. приложить единый `THIRD_PARTY_NOTICES.html` или каталог `licenses/` к образам/дистрибутиву;
3. отмечать изменения Apache-2.0-компонентов, если vendor source модифицирован;
4. не использовать товарные знаки так, будто сторонний правообладатель сертифицировал продукт;
5. не обещать заказчику гарантию от имени OSS-авторов;
6. хранить исходный URL, версию, digest, текст лицензии и дату получения каждого артефакта.

## 6. Source-available и copyleft: отдельные решения

### 6.1. Redpanda v24.3.13

Официальное [описание лицензирования Redpanda 24.3](https://docs.redpanda.com/streaming/24.3/get-started/licensing/overview/) определяет Community Edition как source-available под Redpanda Business Source License. Community features бесплатны, но нельзя предоставлять Redpanda другим как коммерческий streaming/queuing service; код переходит на Apache-2.0 через четыре года после каждого merge. Enterprise Edition использует иной коммерческий режим и требует license key.

«Диспетчер» применяет Redpanda внутри предприятия только как downstream-шину `change_events.v1`, не продаёт Kafka-service третьим лицам. Такое использование предварительно укладывается в Community grant, однако BSL не следует называть Open Source в смысле OSI. Если решение поставляется дочерним обществам как managed service, тиражируется заказчикам или Redpanda становится самостоятельной услугой, юридическая оценка меняется.

Начиная с 24.3 новый кластер автоматически получает 30-дневный trial enterprise features. Поэтому критерий допуска: сохранить вывод `rpk cluster license info`, список активных enterprise features и доказательство Community-only после окончания trial; запретить конфигурации enterprise в IaC; добавить мониторинг срока ключа. При необходимости enterprise-функций получить quote и лицензию, а не эксплуатировать restricted state.

### 6.2. MinIO server

Compose использует отдельный сервер MinIO как опциональный S3-архив сырого Signal. Официальный upstream [описывает community MinIO как AGPL-3.0](https://github.com/minio/minio) и предлагает коммерческую альтернативу. [Страница MinIO compliance](https://min.io/compliance) подчёркивает dual licensing и сетевые/source-code obligations; [документация `mc license info`](https://min.io/docs/minio/linux/reference/minio-mc/mc-license-info.html) различает AGPL и commercial deployment.

AGPL-3.0 разрешает коммерческое использование, но содержит сильный copyleft и специальное требование предоставить Corresponding Source пользователям, взаимодействующим с модифицированной программой по сети. Граница «combined work» между отдельным MinIO server и закрытым Go-приложением через стандартный S3 API юридически не должна определяться разработчиком. Позиция самого вендора трактует proprietary network stacks широко. Поэтому статус — **ПРЕДВАРИТЕЛЬНО, ВЫСОКИЙ ЛИЦЕНЗИОННЫЙ РИСК**.

Допустимые варианты до production:

- купить commercial MinIO/AIStor entitlement с нужным SLA и получить письменное подтверждение прав;
- заменить сервер на утверждённое S3-совместимое хранилище с приемлемой лицензией;
- принять AGPL по заключению юриста, не модифицировать MinIO либо вести полный corresponding source и процесс выдачи исходников, приложить AGPL/offer/source archive;
- отключить опциональный Data Lake: основной ingest и доставка от MinIO не зависят.

Go SDK `minio-go/v7` — отдельный Apache-2.0 компонент; его разрешительная лицензия не меняет лицензию серверного MinIO. Образ закреплён release-тегом, но не digest: перед решением следует извлечь `/licenses`, `mc license info` и OCI metadata именно из реально развёрнутого image.

### 6.3. Docker Engine, Compose и Desktop

Официальная [документация Docker Engine](https://docs.docker.com/engine/install/) называет Engine open-source и указывает Apache-2.0; Docker Compose также имеет [Apache-2.0 LICENSE](https://github.com/docker/compose/blob/main/LICENSE). На Linux-сервере установка Engine/Compose из открытых пакетов сама по себе не требует Docker Desktop subscription.

Docker Desktop — отдельный коммерческий продукт с subscription terms. Документация прямо указывает, что коммерческое использование Desktop в организации более 250 сотрудников **или** с годовой выручкой свыше 10 млн долларов требует платной подписки. Поэтому workstation-разработчиков надо инвентаризировать отдельно: можно купить нужное число Desktop seats либо использовать согласованный Engine/Podman на Linux. Наличие бесплатного Engine не даёт права на бесплатный Desktop.

## 7. Ollama и модели: лицензии разделяются

Ollama runtime имеет [MIT License](https://github.com/ollama/ollama/blob/main/LICENSE). Это не распространяет MIT автоматически на загруженные веса. У каждой модели самостоятельные условия, и даже одно имя в Ollama может указывать на разные digest, quantization и производную модель.

В коде задано имя `log-reader`, а архитектура утверждает, что это локальная Q4_K_M модель на базе `qwen3-coder` 30B. Но репозиторий не содержит Modelfile, исходный digest, `FROM`, LICENSE stanza или вывод `ollama show`. Поэтому связь конкретного `log-reader` с официальными весами **ПРЕДВАРИТЕЛЬНА**. Официальная модель [Qwen3-Coder-30B-A3B-Instruct](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct) опубликована под Apache-2.0, а её [LICENSE](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct/blob/main/LICENSE) разрешает коммерческое использование при выполнении Apache-обязанностей. До допуска нужно доказать, что локальный digest действительно происходит от этой модели и что дополнительные adapters/datasets не добавляют иных условий.

Embedding-модель настроена как `nomic-embed-text`. Официальная карточка [nomic-embed-text-v1.5](https://huggingface.co/nomic-ai/nomic-embed-text-v1.5) указывает Apache-2.0, а [Ollama library](https://ollama.com/library/nomic-embed-text) связывает имя с v1.5. Но `latest` без digest остаётся плавающим, поэтому вывод также предварительный.

Перед релизом выполнить локально:

```text
ollama list
ollama show log-reader
ollama show --modelfile log-reader
ollama show nomic-embed-text
```

Официальный [Ollama Show API](https://docs.ollama.com/api-reference/show-model-details) возвращает license, family, parameter size, quantization и model metadata. Результаты, blob SHA-256, Modelfile, полный текст лицензии и источник весов следует подписать и хранить вместе с release evidence. Запрещается автоматически pull'ить новые модели в production: имя `latest` должно быть заменено внутренним immutable artifact. Отдельно юрист оценивает политику acceptable use, права на outputs, персональные данные и происхождение обучающих данных; Apache-2.0 на weights не гарантирует отсутствие претензий к данным или результатам.

## 8. Целевые отечественные продукты и поддержка

Текущий стенд не содержит Astra Linux, Postgres Pro и Deckhouse. Спецификация называет их целевыми вариантами импортозамещённого инфраструктурного слоя, а не уже купленными лицензиями. Поэтому смета должна иметь две колонки: «пилот на OSS» и «production при выбранной редакции».

**Postgres Pro.** Официальное [лицензионное соглашение Postgres Pro](https://www.postgrespro.ru/products/postgrespro/eula) предусматривает неисключительное право с ограничением числа ядер/серверов/пользователей согласно отдельным соглашениям; обновления и поддержка зависят от лицензии. Для двух узлов primary/replica, резервной площадки и сред dev/test запросить отдельный sizing. Нельзя считать замену строки подключения достаточной для правомерного использования: техническая совместимость и лицензия — разные вопросы. Если остаётся community PostgreSQL/pgvector, покупка Postgres Pro не обязательна, но отсутствуют vendor SLA и сертифицированная редакция.

**Astra Linux.** Официальная [инструкция по активации](https://wiki.astralinux.ru/kb/aktivatsiya-litsenzii-na-astra-linux-174162687.html) говорит, что программная активация не требуется, а подтверждением служат формуляр и лицензионный договор; [приобретение Special Edition](https://wiki.astralinux.ru/ispsystem/dcimanager-admin/gde-kupit-astra-linux-365857239.html) идёт через официальных партнёров. Количество лицензий, уровень защищённости, виртуальные экземпляры и поддержка должны соответствовать topology. Репозиторий не задаёт production OS, поэтому объём **НЕ ОПРЕДЕЛЕН**.

**Deckhouse Kubernetes Platform.** Compose не является Kubernetes deployment. Бесплатная [Community Edition](https://deckhouse.ru/products/kubernetes-platform/community-edition/) заявлена под Apache-2.0 и допускает коммерческое применение без ограничения узлов/срока, но без объёма enterprise support. Коммерческие редакции лицензируются срочно или бессрочно по vCPU, CPU Core либо Server; официальная [страница лицензирования](https://deckhouse.ru/products/kubernetes-platform/licensing-dkp/) указывает, что резервные площадки, dev и test тоже лицензируются, а минимальная закупка при метрике vCPU — 40 vCPU. Enterprise/CSE закупается только при необходимости функций, реестра, ФСТЭК и SLA.

## 9. Сводная лицензионная матрица

| Компонент / фактическая версия | Статус | Лицензия / SPDX | Коммерческое использование | Copyleft / ограничения | Основные обязанности | Закупка | Риск | Первичный источник |
|---|---|---|---|---|---|---|---|---|
| Собственный код `alert-platform` | Не определено | Не задана | По договору/правам автора | Не известно | Оформить права, LICENSE, notices | Договорное оформление | Высокий | Репозиторий: LICENSE отсутствует |
| TrueConf Server, версия не зафиксирована | Не определено | Proprietary EULA | Только в объёме entitlement | Online/PRO/device accounting, activation, support | Проверить договор, сервер, резерв, 87 пользователей и bot | Условно | Высокий | [TrueConf licensing](https://trueconf.com/docs/server/en/admin/license/) |
| Chatbot API, Server 5.5+ | Подтверждено по docs | В составе проприетарного Server API | В рамках server entitlement | Отдельный платёж публично не подтверждён | Получить письменное подтверждение редакции | Не добавлять без quote | Средний | [Connector overview](https://trueconf.com/docs/chatbot-connector/en/overview/) |
| `python-trueconf-bot` >=1.4.2 | Подтверждено для 1.4.2 | BSD-3-Clause-Clear | Да | Permissive | Copyright, license, disclaimer | Нет | Низкий/средний | [PyPI 1.4.2](https://pypi.org/project/python-trueconf-bot/) |
| PostgreSQL 16 | Подтверждено семейство | PostgreSQL | Да | Permissive | Сохранить notice/license | Нет | Низкий/средний | [PostgreSQL License](https://www.postgresql.org/about/licence/) |
| pgvector, версия образа не закреплена | Предварительно | PostgreSQL | Да | Permissive | Notice/license, pin digest | Нет | Средний | [pgvector LICENSE](https://github.com/pgvector/pgvector/blob/master/LICENSE) |
| GLAUTH `latest` | Предварительно | MIT | Да | Permissive | Copyright/license, pin version | Нет | Средний | [GLAUTH LICENSE](https://github.com/glauth/glauth/blob/master/LICENSE) |
| ClickHouse Server `24.8` | Подтверждено семейство | Apache-2.0 | Да | NOTICE, patent clause | License, NOTICE, modified notices, digest | Нет; support optional | Средний | [ClickHouse LICENSE](https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE) |
| Redpanda `v24.3.13` Community | Подтверждено | Redpanda BSL 1.1; не OSI OSS | Да для допустимого внутреннего use | Нельзя commercial streaming/queue service; enterprise features по ключу; conversion через 4 года | Контроль feature usage и trial, текст лицензии | Нет для Community | Средний/высокий | [Redpanda 24.3 licensing](https://docs.redpanda.com/streaming/24.3/get-started/licensing/overview/) |
| MinIO `RELEASE.2025-04-08...` | Предварительно | AGPL-3.0-only либо commercial | Да при соблюдении выбранного режима | Сильный network copyleft; vendor interpretation | Source offer/corresponding source или commercial agreement; exact image evidence | Решить купить/заменить/принять AGPL | Высокий | [точный выпуск](https://github.com/minio/minio/releases/tag/RELEASE.2025-04-08T15-41-24Z), [MinIO upstream](https://github.com/minio/minio), [compliance](https://min.io/compliance) |
| `minio-go/v7 v7.2.1` | Предварительно | Apache-2.0 | Да | Permissive, patent/NOTICE | License/NOTICE | Нет | Низкий/средний | Upstream LICENSE + Go module |
| Docker Engine / Compose | Подтверждено семейство | Apache-2.0 | Да | Permissive; trademarks отдельно | License/NOTICE; scan packages | Нет | Средний | [Moby Engine LICENSE](https://github.com/moby/moby/blob/master/LICENSE), [Compose LICENSE](https://github.com/docker/compose/blob/main/LICENSE) |
| Docker Desktop на рабочих местах | Условно | Docker Subscription Agreement | Да по subscription terms | Платно для крупных организаций по порогам Docker | Учёт seats/юрлица; либо не использовать Desktop | Вероятно да, если используется | Высокий при неучтённом Desktop | [Docker docs](https://docs.docker.com/engine/install/) |
| Ollama, версия хоста не зафиксирована | Предварительно | MIT | Да | Runtime license не покрывает weights | Pin version, copyright/license | Нет | Средний | [Ollama LICENSE](https://github.com/ollama/ollama/blob/main/LICENSE) |
| `log-reader`, заявлен Qwen3-Coder 30B Q4_K_M | Не определено для локального digest | Ожидается Apache-2.0 | Да, если происхождение подтверждено | Model/data/output risks; adapters могут добавить условия | `ollama show`, digest, Modelfile, source, license | Нет по Apache; проверка обязательна | Высокий до аттестации | [Qwen model/LICENSE](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct) |
| `nomic-embed-text` | Предварительно | Apache-2.0 | Да | Имя `latest` плавающее | Pin blob/digest и license | Нет | Средний | [Nomic model card](https://huggingface.co/nomic-ai/nomic-embed-text-v1.5) |
| Go modules из `go.mod/go.sum` | Предварительно | MIT/BSD/Apache и др. | В основном да | Индивидуальные NOTICE/patent | Автоматический scan точных modules | Нет | Средний | Upstream LICENSE каждого module |
| Python packages, диапазоны `>=` | Не определено точно | MIT/BSD/Apache; ldap3 LGPL; psycopg2 LGPL+exception и др. | Предварительно да | LGPL/modification obligations; будущие версии меняются | Lock+hash, scan wheel/sdist, notices | Нет | Высокий до lock | PyPI provenance и upstream LICENSE |
| Frontend packages из lock | Предварительно | Преимущественно MIT; Apache/ISC/BSD/CC-BY | Да | Attribution/data licenses | Scan production bundle, notices | Нет | Средний | `package-lock.json` + upstream LICENSE |
| Python/Go/Node/Alpine/Debian base images | Не определено как совокупность | Множественные | Обычно да | Зависит от пакетов, возможны GPL/LGPL | Syft/ScanCode по финальному digest, source offers | Нет | Высокий без image SBOM | OCI image + package license metadata |
| Postgres Pro target | Условно | PostgreSQL License + Postgres Pro EULA | По купленным правам | Метрика cores/servers/users по соглашению | Quote, сертификат, support и резерв | Только если выбран | Средний/высокий | [Postgres Pro EULA](https://www.postgrespro.ru/products/postgrespro/eula) |
| Astra Linux target | Не определено | Proprietary EULA + OSS composition | По договору | Редакция, экземпляры, формуляр, поддержка | Лицензия через партнёра, учёт VM/резерва | Только если выбран | Средний/высокий | [Astra activation](https://wiki.astralinux.ru/kb/aktivatsiya-litsenzii-na-astra-linux-174162687.html) |
| Deckhouse CE target | Подтверждено по vendor page | Apache-2.0 | Да | CE без коммерческой поддержки | License/NOTICE | Нет | Средний | [Deckhouse CE](https://deckhouse.ru/products/kubernetes-platform/community-edition/) |
| Deckhouse commercial/CSE target | Условно | Commercial | По лицензии | vCPU/core/server; все среды и резерв | Sizing, quote, key, support | Только если выбран | Средний/высокий | [DKP licensing](https://deckhouse.ru/products/kubernetes-platform/licensing-dkp/) |
| GPU/сервер/диски | Подтверждено как не ПО | Не применимо | Не применимо | Драйверы/CUDA отдельно | Учесть EULA software stack | Покупка оборудования, не лицензии | Средний | BOM инфраструктуры |

Оценки риска в матрице — приоритет проверки, а не юридический вердикт. «Низкий/средний» у разрешительной лицензии всё равно предполагает сохранение notices, контроль подлинности и уязвимостей.

## 10. SBOM и автоматический license compliance

Ручная таблица устаревает при первой пересборке. Требуется policy-as-code в CI/CD и доказательный пакет каждого релиза.

### 10.1. Минимальный pipeline

1. Сборка выполняется только из lock-файлов и образов по digest. Python dependencies фиксируются версиями и SHA-256; `glauth:latest` исключается.
2. Для каждого финального OCI image Syft или аналог формирует CycloneDX и SPDX SBOM, включая OS packages, Go binary modules, Python distributions и Node bundle.
3. ScanCode Toolkit, ORT или FOSSology классифицирует LICENSE/NOTICE. `go-licenses`, `pip-licenses` и `license-checker` используются как дополнительные экосистемные источники, но не заменяют полный image scan.
4. CI блокирует неизвестные лицензии, `NOASSERTION`, GPL/AGPL/SSPL/BSL/RCL и custom EULA до явного waiver. Блокировка означает ручное решение, а не автоматический запрет всех copyleft-компонентов.
5. Генерируются `THIRD_PARTY_NOTICES`, каталог полных текстов лицензий и source offer/corresponding source там, где он нужен. NOTICE Apache не сокращается до одной строки SPDX.
6. SBOM, container digest, source commit, dependency locks, model digests, signatures и scan report подписываются и хранятся с релизом не меньше срока эксплуатации плюс срок претензионной давности по политике организации.
7. Отдельно выполняются CVE и provenance checks: лицензия не доказывает безопасность, а отсутствие CVE не доказывает правомерность.

### 10.2. Рекомендуемые правила допуска

**Allow с автоматическим контролем notices:** MIT, BSD-2-Clause, BSD-3-Clause, BSD-3-Clause-Clear, ISC, Apache-2.0, PostgreSQL, PSF-2.0.  
**Review:** LGPL, MPL, EPL, CC-BY, модели с Apache-2.0, container images с множественными лицензиями.  
**Legal approval:** GPL, AGPL, BSL, SSPL, RCL, Elastic License, Commons Clause, неизвестные/custom licenses, проприетарные EULA.  
**Deny до договора:** commercial features без ключа, trial в production, пакет без происхождения или лицензии, модель без digest/Modelfile/license.

Политика применяется и к build dependencies. Если компилятор или генератор не попадает в поставку, его copyleft обычно не переносится на output, но generated code, embedded assets и copied headers могут попадать. Это должен определять scanner и юрист по факту артефакта.

## 11. Процесс закупки и приёмки лицензий

### 11.1. TrueConf

До заказа собрать: версию и edition существующего сервера; license info screenshot/export; число online и PRO rights; фактический peak devices; необходимость federation, резервного сервера и offline activation; SLA поддержки. Направить TrueConf единый вопрос о bot account, Chatbot API, `python-trueconf-bot 1.4.2`, порте 4309 и отсутствии/наличии отдельного Connector entitlement. Результат — не письмо разработчика, а коммерческое предложение/допсоглашение с юридически значимыми условиями.

### 11.2. Production infrastructure

После утверждения deployment topology выбрать один из вариантов:

- OSS: Linux + Docker Engine/Podman + PostgreSQL/pgvector + ClickHouse с собственной поддержкой;
- российский supported stack: Astra Linux, Postgres Pro, коммерческий Deckhouse;
- смешанный режим: Deckhouse CE и community PostgreSQL при внутреннем SLA.

Для каждой среды считать primary, replica, DR, dev, test и временные кластеры. Особенно не переносить метрики между вендорами: Postgres Pro может считать cores/servers/users по соглашению; Deckhouse — vCPU/core/server и лицензирует резервные площадки; Astra — экземпляры и редакции по договору. Полученные ключи, формуляры и акты должны быть связаны с CMDB.

### 11.3. Компоненты повышенного внимания

По MinIO оформить архитектурное решение: commercial, replace, AGPL compliance или disable. По Redpanda зафиксировать Community-only либо купить Enterprise. По Docker провести опрос рабочих станций на Desktop. По моделям создать model registry с digest и лицензией. Эти четыре решения являются gate перед production, даже если приложение функционально проходит тесты.

## 12. Что не удалось однозначно подтвердить

На 10.08.2026 остаются следующие неопределённости:

1. точная редакция, версия, EULA, срок и ёмкость уже установленного TrueConf Server;
2. способ учёта bot account в online/PRO entitlements и письменное подтверждение отсутствия отдельной платы за Chatbot Connector в конкретном договоре;
3. лицензия собственного кода платформы и объём передаваемых заказчику прав;
4. точные Python versions и лицензии всего транзитивного дерева из-за требований `>=` без lock/hashes;
5. точный состав и лицензии OCI-образов с плавающими тегами, включая `glauth:latest`, `pgvector:pg16`, `clickhouse:24.8` и language base images;
6. точная лицензия реально запущенного MinIO image по digest и применимость AGPL к выбранной модели поставки комплекса;
7. фактически активные enterprise features/trial Redpanda v24.3.13;
8. происхождение, digest, Modelfile, адаптеры и полный текст лицензии локальной модели `log-reader`;
9. digest конкретного `nomic-embed-text`, а не только лицензия предполагаемой upstream v1.5;
10. версии и соглашения Ollama, NVIDIA driver, CUDA и container toolkit на production GPU-хосте;
11. необходимость и объём лицензий Postgres Pro, Astra Linux и Deckhouse, поскольку production topology и editions не утверждены;
12. наличие Docker Desktop у разработчиков и число требуемых seats;
13. внешние права заказчика на Zabbix, SolarWinds, AD/LDAP и API-интеграцию;
14. лицензии шрифтов, иконок, изображений и иных frontend assets, если они будут добавлены вне текущего npm lock.

До закрытия пунктов 1–9 релиз можно считать техническим пилотом, но нельзя объявлять полностью лицензионно аттестованной промышленной поставкой.

## 13. Реестр официальных источников

Все ссылки проверены 10.08.2026; при закупке тексты следует сохранить в PDF/архив с хешем, поскольку веб-условия могут измениться.

- TrueConf Server licensing: <https://trueconf.com/docs/server/en/admin/license/>
- TrueConf Server calculator: <https://trueconf.com/server/buy/>
- TrueConf Chatbot Connector overview: <https://trueconf.com/docs/chatbot-connector/en/overview/>
- TrueConf Python SDK repository: <https://github.com/TrueConf/python-trueconf-bot>
- `python-trueconf-bot` 1.4.2 provenance and license: <https://pypi.org/project/python-trueconf-bot/>
- PostgreSQL License: <https://www.postgresql.org/about/licence/>
- pgvector LICENSE: <https://github.com/pgvector/pgvector/blob/master/LICENSE>
- ClickHouse LICENSE: <https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE>
- GLAUTH LICENSE: <https://github.com/glauth/glauth/blob/master/LICENSE>
- Redpanda 24.3 licensing: <https://docs.redpanda.com/streaming/24.3/get-started/licensing/overview/>
- MinIO exact pinned release: <https://github.com/minio/minio/releases/tag/RELEASE.2025-04-08T15-41-24Z>
- MinIO upstream license statement: <https://github.com/minio/minio>
- MinIO compliance and dual licensing: <https://min.io/compliance>
- MinIO deployment license inspection: <https://min.io/docs/minio/linux/reference/minio-mc/mc-license-info.html>
- Moby/Docker Engine LICENSE: <https://github.com/moby/moby/blob/master/LICENSE>
- Docker Compose LICENSE: <https://github.com/docker/compose/blob/main/LICENSE>
- Ollama LICENSE: <https://github.com/ollama/ollama/blob/main/LICENSE>
- Ollama model metadata API: <https://docs.ollama.com/api-reference/show-model-details>
- Qwen3-Coder 30B model and LICENSE: <https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct>
- Nomic Embed Text v1.5 model card: <https://huggingface.co/nomic-ai/nomic-embed-text-v1.5>
- Postgres Pro EULA: <https://www.postgrespro.ru/products/postgrespro/eula>
- Astra Linux license confirmation: <https://wiki.astralinux.ru/kb/aktivatsiya-litsenzii-na-astra-linux-174162687.html>
- Deckhouse Community Edition: <https://deckhouse.ru/products/kubernetes-platform/community-edition/>
- Deckhouse commercial licensing: <https://deckhouse.ru/products/kubernetes-platform/licensing-dkp/>
