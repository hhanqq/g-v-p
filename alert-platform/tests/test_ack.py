"""Тесты отслеживания реакции на инцидент (раздел «Сценарии», Этап 3) —
packages/common/ack.py. Только БД-логика, TrueConf-бот не поднимаем
(reply_message_id имитируется напрямую)."""
from datetime import datetime

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from packages.common.ack import mark_acknowledged
from packages.models.db import Base, Notification, Problem

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.fixture()
def session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    with Session(engine) as s:
        problem = Problem(dedup_key="x", status="OPEN", symptom_class="node_down",
                           opened_at=T0, last_seen_at=T0, repeat_count=1, toggle_count=0)
        s.add(problem)
        s.flush()
        s.add(Notification(problem_id=problem.id, type="NEW", chat_id="chat-1",
                            message_id="msg-new-1", status="sent", created_at=T0))
        s.add(Notification(problem_id=problem.id, type="SUPPLEMENT", chat_id="chat-1",
                            message_id="msg-sup-1", status="sent", created_at=T0))
        s.commit()
        yield s, problem.id


def test_marks_problem_acknowledged_on_reply_to_new(session):
    s, problem_id = session
    result = mark_acknowledged(s, reply_message_id="msg-new-1", by_username="tester")
    assert result is True
    problem = s.get(Problem, problem_id)
    assert problem.acknowledged_by == "tester"
    assert problem.acknowledged_at is not None


def test_reply_to_unknown_message_id_does_nothing(session):
    s, problem_id = session
    result = mark_acknowledged(s, reply_message_id="does-not-exist", by_username="tester")
    assert result is False
    assert s.get(Problem, problem_id).acknowledged_at is None


def test_reply_to_non_new_notification_does_not_acknowledge(session):
    """Реакция «на инцидент» — только ответ на исходное NEW, не на
    любое сообщение по проблеме (например, SUPPLEMENT)."""
    s, problem_id = session
    result = mark_acknowledged(s, reply_message_id="msg-sup-1", by_username="tester")
    assert result is False
    assert s.get(Problem, problem_id).acknowledged_at is None


def test_second_reply_does_not_overwrite_first_ack(session):
    s, problem_id = session
    mark_acknowledged(s, reply_message_id="msg-new-1", by_username="first_responder")
    first_ack_at = s.get(Problem, problem_id).acknowledged_at

    result = mark_acknowledged(s, reply_message_id="msg-new-1", by_username="second_responder")
    assert result is False
    problem = s.get(Problem, problem_id)
    assert problem.acknowledged_by == "first_responder"
    assert problem.acknowledged_at == first_ack_at
