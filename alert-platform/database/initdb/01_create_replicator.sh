#!/bin/sh
# Устойчивость к будущему чистому передеплою (docker-compose down -v +
# up): исполняется ТОЛЬКО официальным entrypoint'ом postgres при
# инициализации ПУСТОГО тома, никак не затрагивает уже работающий
# primary (там роль создана вручную) — раздел «Инфраструктура и
# масштабируемость» кейса, резервирование (ops/postgres-replica/).
# Пароль — тот же ${VAR:-fallback}-паттерн, что уже принят в проекте
# для demo-стенда (Postgres/ClickHouse/MinIO), сеть закрыта извне.
set -e
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
    DO \$\$
    BEGIN
      IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'replicator') THEN
        CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATOR_PASSWORD:-replicator_demo_pw}';
      END IF;
    END
    \$\$;
SQL
