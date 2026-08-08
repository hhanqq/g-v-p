"""Тесты запроса ИИ-разбора алерта по инициативе сотрудника —
packages/common/ai_request.py. Сам разбор делает Go delivery-planner;
здесь проверяется только узкий Python-контракт фиксации заявки."""
from datetime import datetime

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session

from packages.common.ai_request import request_ai_analysis, wants_ai_help
from packages.models.db import AiAnalysisRequest, Base, Notification, Problem

T0 = datetime(2026, 8, 6, 10, 0, 0)


@pytest.mark.parametrize("text", ["анализ", "Анализ", "  ии  ", "AI", "/анализ", "помощь", "разбор"])
def test_wants_ai_help_matches_known_triggers_case_insensitively(text):
    assert wants_ai_help(text) is True


@pytest.mark.parametrize("text", ["", "спасибо", "взял", "не мой", "анализ пожалуйста"])
def test_wants_ai_help_rejects_everything_else(text):
    assert wants_ai_help(text) is False


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
        s.commit()
        yield s, problem.id


def test_request_ai_analysis_queues_a_pending_request(session):
    s, problem_id = session
    result = request_ai_analysis(s, reply_message_id="msg-new-1", by_username="tester")
    assert result is True
    request = s.execute(select(AiAnalysisRequest)).scalars().one()
    assert request.problem_id == problem_id
    assert request.requested_by == "tester"
    assert request.status == "pending"


def test_request_ai_analysis_on_unknown_message_id_does_nothing(session):
    s, _ = session
    result = request_ai_analysis(s, reply_message_id="does-not-exist", by_username="tester")
    assert result is False
    assert s.execute(select(AiAnalysisRequest)).scalars().first() is None


def test_request_ai_analysis_does_not_duplicate_a_pending_request(session):
    """Повторный тап 'анализ' на ту же проблему, пока предыдущая заявка ещё
    не обработана Go-планировщиком, не должен спамить Ollama второй заявкой —
    но пользователь всё равно получает подтверждение (result=True)."""
    s, _ = session
    first = request_ai_analysis(s, reply_message_id="msg-new-1", by_username="tester")
    second = request_ai_analysis(s, reply_message_id="msg-new-1", by_username="tester")
    assert first is True
    assert second is True
    assert s.execute(select(AiAnalysisRequest)).scalars().all().__len__() == 1
