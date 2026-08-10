#!/bin/sh
# Раздел «Инфраструктура и масштабируемость» кейса — резервирование.
# Идемпотентность по PG_VERSION — та же конвенция, что использует
# официальный образ postgres для решения "нужен ли initdb": пустой
# $PGDATA -> один раз клонируем primary через pg_basebackup (-R сам
# пишет standby.signal + primary_conninfo), непустой -> просто
# стартуем. НЕ "exec postgres" напрямую — официальный entrypoint
# делает chown/permission-setup и понижение привилегий до postgres,
# без которого сервер откажется стартовать от root на свежем volume.
set -e

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "postgres-replica: data dir empty, bootstrapping via pg_basebackup from $PRIMARY_HOST"
  rm -rf "${PGDATA:?}"/*
  until pg_basebackup -h "$PRIMARY_HOST" -U "$REPLICATION_USER" -D "$PGDATA" -Fp -Xs -P -R; do
    echo "postgres-replica: pg_basebackup failed, retry in 5s"
    sleep 5
  done
  echo "postgres-replica: bootstrap complete"
fi

exec docker-entrypoint.sh postgres
