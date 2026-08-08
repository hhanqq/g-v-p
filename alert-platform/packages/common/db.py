"""Подключение к БД. Раздел 15.1: демонстрационный стенд — PostgreSQL,
целевой контур — Postgres Pro (замена строкой подключения, без правок кода).
"""
from __future__ import annotations

import os

from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker

DATABASE_URL = os.environ.get(
    "DATABASE_URL", "postgresql+psycopg2://alert:alert@localhost:5432/alert_platform"
)

engine = create_engine(DATABASE_URL, pool_pre_ping=True)
SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False)


def get_session() -> Session:
    return SessionLocal()
