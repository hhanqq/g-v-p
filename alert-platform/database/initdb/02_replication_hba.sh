#!/bin/sh
# Пара к 01_create_replicator.sh — та же логика "только при чистой
# инициализации", тот же принцип, что уже применён вручную на живом
# primary (SECURITY.md, раздел «История изменений»). Подсеть — сеть
# compose по умолчанию для этого проекта (alert-platform_default).
set -e
echo "host replication replicator ${REPLICA_NETWORK_CIDR:-172.18.0.0/16} scram-sha-256" >> "$PGDATA/pg_hba.conf"
