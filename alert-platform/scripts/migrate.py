"""Минимальный транзакционный runner SQL-миграций для демо-стенда."""
from __future__ import annotations

from pathlib import Path

from sqlalchemy import text

from packages.common.db import engine


ROOT = Path(__file__).resolve().parents[1]
MIGRATIONS_DIR = ROOT / "database" / "migrations"


def migrate() -> None:
    with engine.begin() as connection:
        connection.execute(text(
            "CREATE TABLE IF NOT EXISTS schema_migrations ("
            "version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)"
        ))
        applied = set(connection.execute(text("SELECT version FROM schema_migrations")).scalars())

        for path in sorted(MIGRATIONS_DIR.glob("*.sql")):
            if path.name in applied:
                continue
            sql = path.read_text(encoding="utf-8")
            # Файлы миграций контролируются репозиторием и выполняются в
            # одной транзакции; psycopg2 поддерживает несколько SQL-команд.
            connection.exec_driver_sql(sql)
            connection.execute(
                text("INSERT INTO schema_migrations(version) VALUES (:version)"),
                {"version": path.name},
            )
            print(f"migrate: applied {path.name}")


if __name__ == "__main__":
    migrate()
