# ADP — Alert Data Platform

[![CI](https://github.com/hhanqq/g-v-p/actions/workflows/ci.yml/badge.svg)](https://github.com/hhanqq/g-v-p/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Python](https://img.shields.io/badge/Python-3.12-3776AB?logo=python&logoColor=white)
![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=111111)
![TypeScript](https://img.shields.io/badge/TypeScript-5-3178C6?logo=typescript&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)
![Docker Compose](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)
![TrueConf](https://img.shields.io/badge/Delivery-TrueConf-ED1C24)
![Ollama](https://img.shields.io/badge/AI-Ollama-000000?logo=ollama&logoColor=white)
![Redpanda](https://img.shields.io/badge/Streaming-Redpanda-EF4B4B)
![ClickHouse](https://img.shields.io/badge/Analytics-ClickHouse-FFCC01?logo=clickhouse&logoColor=111111)
![MinIO](https://img.shields.io/badge/Storage-MinIO-C72E49?logo=minio&logoColor=white)

Платформа управления оповещениями о промышленных инцидентах. ADP принимает события из систем мониторинга, объединяет связанные сигналы, определяет ответственных и доставляет понятные уведомления с автоматической эскалацией.

## Быстрый запуск

Понадобятся Docker Engine 24+ и Docker Compose v2.

```bash
cd alert-platform
cp .env.example .env

docker compose up -d --build \
  postgres postgres-replica ldap migrate kb-index \
  gateway pipeline-worker delivery-planner admin-console demo-runner \
  deprovision-worker redpanda clickhouse clickhouse-migrate minio \
  changelog-worker delivery-email mailhog
```

После запуска:

- веб-консоль: <http://127.0.0.1:8090/>;
- вход: `admin1` / `admin123`;
- проверка API: `curl http://127.0.0.1:8081/api/v1/health`.

Для доставки в TrueConf заполните `TRUECONF_*` в `.env` и запустите:

```bash
docker compose up -d --build delivery-trueconf
```

Подробная инструкция и демо-данные — в [README платформы](alert-platform/README.md).
