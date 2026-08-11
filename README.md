# ADP — Alert Data Platform

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
