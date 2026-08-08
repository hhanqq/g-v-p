"""Тест журнала аудита — раздел «Безопасность»."""
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.common.audit import log_action
from packages.models.db import AuditLog, Base


def test_log_action_persists_all_fields():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        log_action(session, actor="admin1", action="source_add", target="zbx-newsite-01",
                   detail="system=zabbix site=gpn-newsite")
        entry = session.query(AuditLog).one()
        assert entry.actor == "admin1"
        assert entry.action == "source_add"
        assert entry.target == "zbx-newsite-01"
        assert "gpn-newsite" in entry.detail
        assert entry.created_at is not None
