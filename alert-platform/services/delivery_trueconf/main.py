"""Бот TrueConf — раздел 9, M4+M5: доставка NEW/CLOSURE/SUPPLEMENT по
подпискам (раздел 8), а не в один захардкоженный тестовый чат.

Раздел 9.4 (ответы на уведомления) реализован частично, Этап 3: любой
ответ (reply) на NEW засчитывается как факт реакции на инцидент
(`packages.common.ack.mark_acknowledged`, используется узлом-развилкой
«Проверка реакции» в сценариях). Полная трёхвариантная семантика
(«взял»/«не мой»/«следствие», с перемаршрутизацией и диалогом
корреляции) остаётся вне объёма — это M8/M9.

Адресация (раздел 8, M5): для каждой Problem `resolve_recipients()`
(packages/common/routing.py) вычисляет список TrueConf-логинов подписчиков
по владению CMDB (CmdbOwnership) и фильтрам их подписок. Личный чат на
каждого получателя создаётся лениво и кэшируется на время жизни процесса
(_chat_cache) — TrueConf не выдаёт chat_id заранее, только через
create_personal_chat().

Раздел 9.8 — надёжность доставки:
  1. Исходящая очередь: Notification создаётся со status='queued' ДО
     вызова send_message, статус обновляется после ответа сервера.
  2. Идемпотентность: UniqueConstraint(problem_id, type, chat_id) в БД —
     второй проход воркера по тому же problem+получателю не создаст
     вторую отправку, но разным получателям одна Problem уходит отдельно.
  4. Самомониторинг: on_health_check пишет в лог (реального дашборда,
     куда это могло бы стекать, пока нет — раздел 14.5).
"""
from __future__ import annotations

import asyncio
import os
import secrets
from datetime import datetime

from sqlalchemy import select
from trueconf import Bot, Dispatcher, Router
from trueconf.enums.parse_mode import ParseMode
from trueconf.types import Message
from trueconf.utils._ssl import _build_ssl_context
from trueconf.utils._token import _get_auth_token

from packages.ai.recommend import checklist_for, recommend_remediation
from packages.common.ack import mark_acknowledged
from packages.common.db import engine, get_session
from packages.common.routing import owning_service_ids, owning_subsidiaries, resolve_recipients
from packages.models.db import (Base, CmdbObject, CmdbService, CmdbServiceObject,
                                 Event, Incident, IncidentProblem, Notification, Problem, Scenario,
                                 ScenarioRun, Signal, SLARule, SlaBreachNotice, Subscriber, Subscription)
from packages.scenarios.engine import DECISION_TYPES, advance, matches_condition, parse_graph
from services.delivery_trueconf.llm_summary import build_prompt, summarize_incident
from services.delivery_trueconf.templates import (format_duration, render_closure,
                                                    render_duplicate_note, render_new,
                                                    render_scenario_notify, render_sla_breach,
                                                    render_supplement)

TRUECONF_SERVER = os.environ["TRUECONF_SERVER"]
TRUECONF_BOT_USERNAME = os.environ["TRUECONF_BOT_USERNAME"]
TRUECONF_BOT_PASSWORD = os.environ["TRUECONF_BOT_PASSWORD"]
TRUECONF_TEST_RECIPIENT = os.environ["TRUECONF_TEST_RECIPIENT"]
POLL_INTERVAL_S = float(os.environ.get("DELIVERY_POLL_INTERVAL_S", "5"))
# Наш сервер поднят без HTTPS/443 (раздел 20 п.2 — версию и конфигурацию
# сервера нужно проверять до начала разработки; здесь конфигурация внесла
# правку в подключение). Bot.from_credentials() жёстко ходит за токеном на
# https://<server>:443 независимо от переданных https/web_port — поэтому
# токен получаем вручную через тот же http/4309, на котором реально
# слушает tc_bridge, и уже с ним конструируем Bot() напрямую.
TRUECONF_HTTPS = os.environ.get("TRUECONF_HTTPS", "false").lower() == "true"
TRUECONF_PORT = int(os.environ.get("TRUECONF_PORT", "4309"))

PUBLIC_CONSOLE_URL = os.environ.get("TRUECONF_PUBLIC_CONSOLE_URL",
                                     "https://xn--80aebrvrg.xn--p1acf/console")

router = Router()


def _get_or_create_subscriber_with_token(session, username: str) -> Subscriber:
    """Единственное место, где заводится Subscriber с access_token —
    вызывается только отсюда (реакция на сообщение ОТ этого же логина в
    TrueConf), никогда с веб-страницы личного кабинета напрямую (раздел
    20 п.11 — личность подтверждает канал, не URL-параметр)."""
    subscriber = session.execute(
        select(Subscriber).where(Subscriber.trueconf_username == username)
    ).scalars().first()
    if subscriber is None:
        subscriber = Subscriber(trueconf_username=username, access_token=secrets.token_urlsafe(24),
                                 created_at=datetime.utcnow())
        session.add(subscriber)
        session.commit()
    return subscriber


@router.message()
async def on_any_message(message: Message):
    if message.reply_message_id:
        # Раздел «Сценарии», Этап 3 — минимальный срез раздела 9.4: любой
        # ответ на NEW-уведомление засчитывается как факт реакции на
        # инцидент (узел-развилка «Проверка реакции»). Полная семантика
        # «взял»/«не мой»/«следствие» (M8/M9) сюда не входит.
        username = message.author.id.split("@", 1)[0]
        with get_session() as session:
            acknowledged = mark_acknowledged(session, message.reply_message_id, username)
        if acknowledged:
            await message.answer("Принято, отмечено как реакция на инцидент.")
        return

    text = (message.text or "").strip().lower()
    if text in ("/старт", "/start"):
        await message.answer(
            "Бот «Диспетчер» на связи. Присылаю уведомления NEW/CLOSURE по проблемам "
            "и инцидентам.\nЧтобы настроить, какие уведомления вам приходят — команда "
            "/кабинет.\nЧтобы посмотреть текущие алерты и их последовательность — "
            "команда /алерты.\nОтвет (reply) на NEW-уведомление засчитывается как "
            "реакция на инцидент; полная обработка команд в ответах — отдельный этап "
            "(раздел 9.4)."
        )
    elif text in ("/кабинет", "/подписки", "/subscriptions"):
        username = message.author.id.split("@", 1)[0]
        with get_session() as session:
            subscriber = _get_or_create_subscriber_with_token(session, username)
            link = f"{PUBLIC_CONSOLE_URL}/me/{username}/?token={subscriber.access_token}"
        await message.answer(
            f"Ваш личный кабинет подписок (раздел 8): {link}\n"
            f"Ссылка привязана к вашему TrueConf-логину — не пересылайте её."
        )
    elif text in ("/алерты", "/текущие", "/alerts"):
        username = message.author.id.split("@", 1)[0]
        with get_session() as session:
            subscriber = _get_or_create_subscriber_with_token(session, username)
            link = f"{PUBLIC_CONSOLE_URL}/alerts/{username}/?token={subscriber.access_token}"
        await message.answer(
            f"Ваши текущие алерты и их последовательность: {link}\n"
            f"Ссылка привязана к вашему TrueConf-логину — не пересылайте её."
        )
    else:
        await message.answer("Команды: /старт, /кабинет (настройка подписок), "
                              "/алерты (текущие алерты и их последовательность)")


async def on_health_check(status: dict) -> None:
    print(f"delivery_trueconf: health_check {status}")


def _original_body_for(session, problem: Problem) -> tuple[str, str]:
    event = session.execute(
        select(Event).where(Event.problem_id == problem.id).order_by(Event.id.asc()).limit(1)
    ).scalars().first()
    if event is None:
        return "(оригинал не найден)", "unknown"
    signal = session.get(Signal, event.signal_id)
    return signal.raw_body, signal.source_system


def _service_name_for(session, object_id: str | None) -> str | None:
    if not object_id:
        return None
    return session.execute(
        select(CmdbService.name)
        .join(CmdbServiceObject, CmdbServiceObject.service_id == CmdbService.id)
        .where(CmdbServiceObject.object_id == object_id)
        .limit(1)
    ).scalar_one_or_none()


def _object_display(session, object_id: str | None) -> str:
    if not object_id:
        return "неизвестный объект"
    obj = session.get(CmdbObject, object_id)
    return obj.name if obj else object_id


async def notify_new(bot: Bot, session, problem: Problem, chat_id: str, username: str) -> None:
    notification = Notification(problem_id=problem.id, type="NEW", chat_id=chat_id,
                                 status="queued", created_at=datetime.utcnow())
    session.add(notification)
    session.commit()  # раздел 9.8 п.1 — в очереди ДО отправки

    original_body, source_system = _original_body_for(session, problem)
    # Этап 4 — ссылка на «мои текущие алерты» прямо в самом уведомлении
    # (не только по команде /алерты): переиспользуем тот же токен, что и
    # у личного кабинета (_get_or_create_subscriber_with_token).
    subscriber = _get_or_create_subscriber_with_token(session, username)
    alerts_link = f"{PUBLIC_CONSOLE_URL}/alerts/{username}/?token={subscriber.access_token}"
    text = render_new(
        problem_id=problem.id, incident_id=problem.incident_id, priority=problem.priority,
        object_name=_object_display(session, problem.object_id), site_name=problem.site or "?",
        service_name=_service_name_for(session, problem.object_id),
        source_system=source_system, original_body=original_body, symptom_class=problem.symptom_class,
        ai_root_cause_hypothesis=problem.ai_root_cause_hypothesis, alerts_link=alerts_link,
    )
    try:
        resp = await bot.send_message(chat_id=chat_id, text=text, parse_mode=ParseMode.HTML)
        notification.message_id = resp.message_id
        notification.status = "sent"
        notification.sent_at = datetime.utcnow()
    except Exception as exc:  # noqa: BLE001 — раздел И5: сбой доставки не должен ронять цикл
        notification.status = "failed"
        notification.error = str(exc)[:2000]
    session.commit()


async def notify_duplicate(bot: Bot, session, problem: Problem) -> None:
    """Раздел 4.1 — problem.duplicate_of_problem_id уже проставлен
    воркером (packages/ai/dedup.py). Пометка уходит в те же чаты, что уже
    получили NEW по ОРИГИНАЛУ (не пересчитываем recipients заново — дубль
    адресован ровно тем же людям, что и событие, которое он дублирует),
    ответом на их экземпляр NEW, чтобы попасть в ту же ветку переписки."""
    original_news = session.execute(
        select(Notification).where(Notification.problem_id == problem.duplicate_of_problem_id,
                                    Notification.type == "NEW", Notification.status == "sent")
    ).scalars().all()
    if not original_news:
        return  # оригинал ещё не доставлен — подождём следующего тика
    already_chat_ids = set(session.execute(
        select(Notification.chat_id).where(Notification.problem_id == problem.id,
                                             Notification.type == "DUPLICATE_NOTE")
    ).scalars().all())
    _, source_system = _original_body_for(session, problem)
    original_problem = session.get(Problem, problem.duplicate_of_problem_id)
    for orig_new in original_news:
        if orig_new.chat_id in already_chat_ids:
            continue
        notification = Notification(problem_id=problem.id, type="DUPLICATE_NOTE",
                                     chat_id=orig_new.chat_id, status="queued",
                                     created_at=datetime.utcnow())
        session.add(notification)
        session.commit()
        text = render_duplicate_note(
            duplicate_problem_id=problem.id, original_problem_id=problem.duplicate_of_problem_id,
            original_incident_id=original_problem.incident_id if original_problem else None,
            source_system=source_system,
        )
        try:
            resp = await bot.send_message(chat_id=orig_new.chat_id, text=text, parse_mode=ParseMode.HTML,
                                           reply_message_id=orig_new.message_id)
            notification.message_id = resp.message_id
            notification.status = "sent"
            notification.sent_at = datetime.utcnow()
        except Exception as exc:  # noqa: BLE001
            notification.status = "failed"
            notification.error = str(exc)[:2000]
        session.commit()


async def notify_closure(bot: Bot, session, problem: Problem, new_notification: Notification) -> None:
    notification = Notification(problem_id=problem.id, type="CLOSURE",
                                 chat_id=new_notification.chat_id, status="queued",
                                 created_at=datetime.utcnow())
    session.add(notification)
    session.commit()

    duration = format_duration((problem.resolved_at - problem.opened_at).total_seconds())
    text = render_closure(
        problem_id=problem.id, incident_id=problem.incident_id, resolved_at=problem.resolved_at,
        duration_text=duration, closed_by_reconciliation=problem.closed_by_reconciliation,
    )
    try:
        # Связь сообщений одного инцидента через reply_message_id (раздел 9.3).
        resp = await bot.send_message(chat_id=new_notification.chat_id, text=text,
                                       parse_mode=ParseMode.HTML,
                                       reply_message_id=new_notification.message_id)
        notification.message_id = resp.message_id
        notification.status = "sent"
        notification.sent_at = datetime.utcnow()
    except Exception as exc:  # noqa: BLE001
        notification.status = "failed"
        notification.error = str(exc)[:2000]
    session.commit()


async def send_supplement(bot: Bot, *, incident_id: int, root_problem_id: int,
                           notification_id: int, chat_id: str, reply_message_id: str | None) -> None:
    """Отправка SUPPLEMENT в фоне — раздел 13: вызов LLM может занимать
    десятки секунд (холодный старт модели), поэтому не должен блокировать
    основной цикл delivery_tick, где идут обычные NEW/CLOSURE."""
    with get_session() as session:
        root = session.get(Problem, root_problem_id)
        symptom_rows = session.execute(
            select(Problem, IncidentProblem.rule_id)
            .join(IncidentProblem, IncidentProblem.problem_id == Problem.id)
            .where(IncidentProblem.incident_id == incident_id, IncidentProblem.role == "symptom")
        ).all()
        symptoms = [(_object_display(session, p.object_id), p.symptom_class) for p, _ in symptom_rows]
        rule_ids = sorted({rid for _, rid in symptom_rows if rid})  # напр. "corr-114" — раздел 6.4
        services = {s for p, _ in symptom_rows for s in
                     ([_service_name_for(session, p.object_id)] if _service_name_for(session, p.object_id) else [])}

        prompt = build_prompt(
            root_symptom=root.symptom_class, root_object=_object_display(session, root.object_id),
            root_site=root.site or "?", opened_at=f"{root.opened_at:%Y-%m-%d %H:%M:%S}",
            symptoms=symptoms, rule_names=rule_ids,
        )
        ai_summary = await summarize_incident(prompt)

        # ИИ-сценарий «рекомендации из базы знаний» (раздел 5) — та же
        # деградация, что и у саммари: ИИ недоступна → показываем чек-лист
        # как есть (render_supplement сам решает, что показать).
        root_object_name = _object_display(session, root.object_id)
        ai_recommendation = await recommend_remediation(
            symptom_class=root.symptom_class, object_name=root_object_name,
            site=root.site or "?", n_related=len(symptoms),
        )
        checklist_fallback = None if ai_recommendation else checklist_for(root.symptom_class)

        text = render_supplement(
            problem_id=root.id, incident_id=incident_id, root_object=root_object_name,
            root_symptom_class=root.symptom_class, opened_at=root.opened_at, n_symptoms=len(symptoms),
            n_services=len(services), rule_names=rule_ids, ai_summary=ai_summary,
            ai_recommendation=ai_recommendation, checklist_fallback=checklist_fallback,
        )

        notification = session.get(Notification, notification_id)
        # Раздел 13 — сохраняем реально сгенерированный текст в БД, не
        # только отправляем в TrueConf: demo-страница читает отсюда, а не
        # ходит в чат бота за примерами (раздел «Использование ИИ»).
        notification.ai_summary = ai_summary
        notification.ai_recommendation = ai_recommendation
        try:
            resp = await bot.send_message(chat_id=chat_id, text=text, parse_mode=ParseMode.HTML,
                                           reply_message_id=reply_message_id)
            notification.message_id = resp.message_id
            notification.status = "sent"
            notification.sent_at = datetime.utcnow()
        except Exception as exc:  # noqa: BLE001
            notification.status = "failed"
            notification.error = str(exc)[:2000]
        session.commit()


def claim_pending_supplements(session) -> list[tuple[int, int, int, str, str | None]]:
    """Раздел 3.3: SUPPLEMENT отправляется, когда определена первопричина —
    здесь упрощено до "инцидент только что сформирован коррелятором".
    Клеймится синхронно (Notification со статусом queued), чтобы следующий
    тик delivery_tick не подхватил тот же инцидент повторно, пока фоновая
    задача с LLM ещё работает. Раздел 8 (M5): у root_problem теперь может
    быть несколько NEW-получателей (разные подписчики) — SUPPLEMENT идёт
    ответом в каждый из этих чатов отдельно, не только в первый попавшийся."""
    already_pairs = {
        (pid, cid) for pid, cid in session.execute(
            select(Notification.problem_id, Notification.chat_id)
            .where(Notification.type == "SUPPLEMENT")
        ).all()
    }
    root_problem_ids = set(session.execute(select(Incident.root_problem_id)).scalars().all())
    if not root_problem_ids:
        return []
    incident_by_root = {i.root_problem_id: i for i in session.execute(
        select(Incident).where(Incident.root_problem_id.in_(root_problem_ids))
    ).scalars().all()}

    claimed = []
    root_news = session.execute(
        select(Notification).where(Notification.problem_id.in_(root_problem_ids),
                                    Notification.type == "NEW", Notification.status == "sent")
    ).scalars().all()
    for root_new in root_news:
        if (root_new.problem_id, root_new.chat_id) in already_pairs:
            continue
        incident = incident_by_root[root_new.problem_id]
        notification = Notification(problem_id=root_new.problem_id, type="SUPPLEMENT",
                                     chat_id=root_new.chat_id, status="queued",
                                     created_at=datetime.utcnow())
        session.add(notification)
        session.flush()
        claimed.append((incident.id, root_new.problem_id, notification.id, root_new.chat_id, root_new.message_id))
    session.commit()
    return claimed


async def send_scenario_notify(bot: Bot, session, *, problem: Problem, scenario: Scenario,
                                chat_id: str, is_escalation: bool) -> None:
    notification = Notification(problem_id=problem.id, type="SCENARIO", chat_id=chat_id,
                                 status="queued", created_at=datetime.utcnow())
    session.add(notification)
    session.commit()  # раздел 9.8 п.1 — в очереди ДО отправки, тот же принцип, что и у NEW/CLOSURE
    text = render_scenario_notify(
        problem_id=problem.id, incident_id=problem.incident_id, scenario_name=scenario.name,
        object_name=_object_display(session, problem.object_id), is_escalation=is_escalation,
    )
    try:
        resp = await bot.send_message(chat_id=chat_id, text=text, parse_mode=ParseMode.HTML)
        notification.message_id = resp.message_id
        notification.status = "sent"
        notification.sent_at = datetime.utcnow()
    except Exception as exc:  # noqa: BLE001
        notification.status = "failed"
        notification.error = str(exc)[:2000]
    session.commit()


def _scenario_facts(session, graph, problem: Problem) -> dict[str, bool]:
    """Считает булев факт на каждый узел-развилку графа ДО вызова
    advance() — сама advance() остаётся чистой функцией без обращения к
    БД (см. packages/scenarios/engine.py)."""
    facts: dict[str, bool] = {}
    recipients: list[str] | None = None
    for node_id, step in graph.nodes.items():
        step_type = step.get("type")
        if step_type not in DECISION_TYPES:
            continue
        if step_type == "ack_check":
            facts[node_id] = problem.acknowledged_at is not None
        elif step_type == "subscription_check":
            if recipients is None:
                recipients = resolve_recipients(session, problem)
            employee_id = step.get("employee_id")
            if employee_id:
                subscriber = session.get(Subscriber, employee_id)
                facts[node_id] = bool(subscriber and subscriber.trueconf_username in recipients)
            else:
                facts[node_id] = bool(recipients)
    return facts


async def run_scenarios(bot: Bot, session, domain: str) -> None:
    """Раздел «Сценарии» — packages/scenarios/engine.py на каждый тик
    продвигает активные сценарии по открытым проблемам, включая узлы-
    развилки (Проверка реакции/Проверка подписки, Этап 3). В отличие от
    SUPPLEMENT здесь нет обращения к LLM, поэтому фоновая задача не нужна —
    отправка идёт синхронно внутри самого тика (см. план платформы)."""
    now = datetime.utcnow()
    active_scenarios = session.execute(select(Scenario).where(Scenario.status == "active")).scalars().all()
    if not active_scenarios:
        return
    open_problems = session.execute(
        select(Problem).where(Problem.status.in_(("OPEN", "FLAPPING")))
    ).scalars().all()
    if not open_problems:
        return

    for scenario in active_scenarios:
        graph = parse_graph(scenario.graph_json)
        if graph is None:
            continue  # раздел И5 — сценарий с графом, не проходящим валидацию, просто не исполняется
        condition = graph.nodes[graph.root_id]
        for problem in open_problems:
            run = session.execute(
                select(ScenarioRun).where(ScenarioRun.scenario_id == scenario.id,
                                           ScenarioRun.problem_id == problem.id)
            ).scalars().first()
            if run is None:
                owning = owning_subsidiaries(session, problem)
                if not matches_condition(condition, problem, owning):
                    continue
                run = ScenarioRun(scenario_id=scenario.id, problem_id=problem.id,
                                   current_node_id=graph.root_id, status="running",
                                   step_entered_at=now, created_at=now)
                session.add(run)
                session.flush()
            if run.status != "running":
                continue

            facts = _scenario_facts(session, graph, problem)
            outcome, step, new_node_id, new_entered_at = advance(
                run.current_node_id, run.step_entered_at, graph, problem.status, facts, now,
            )
            if outcome == "wait":
                run.current_node_id, run.step_entered_at = new_node_id, new_entered_at
                session.commit()
                continue
            if outcome == "done":
                run.status, run.current_node_id = "done", new_node_id
                session.commit()
                continue

            # outcome == "notify" — шаг сразу помечается пройденным (до отправки),
            # чтобы сбой сети не заставил повторять его на каждом следующем тике.
            # С ветвлением позиция узла в графе больше не определяет однозначно
            # "первое уведомление или эскалация" — считаем явно (notified_count).
            is_escalation = run.notified_count > 0
            run.notified_count += 1
            if new_node_id is None:
                run.status = "done"  # дальше графа нет — прогон завершён этим уведомлением
            else:
                run.current_node_id, run.step_entered_at = new_node_id, new_entered_at
            session.commit()

            employee_id = step.get("employee_id")
            subscriber = session.get(Subscriber, employee_id) if employee_id else None
            if subscriber is None or not subscriber.active:
                continue  # некого уведомлять — раздел И5, не ошибка, просто пропуск шага
            try:
                chat_id = await get_chat_id(bot, subscriber.trueconf_username, domain)
                await send_scenario_notify(bot, session, problem=problem, scenario=scenario,
                                            chat_id=chat_id, is_escalation=is_escalation)
            except Exception as exc:  # noqa: BLE001
                print(f"delivery_trueconf: ошибка SCENARIO для problem={problem.id} "
                      f"scenario={scenario.id}: {exc}")


def _matching_sla_rule(session, problem: Problem) -> SLARule | None:
    """Самое специфичное подходящее правило: филиал+сервис точнее, чем
    просто приоритет — та же идея специфичности, что и в resolve_recipients
    (packages/common/routing.py), только считаем не получателей, а порог."""
    if not problem.priority:
        return None
    owning_subs = owning_subsidiaries(session, problem)
    owning_services = owning_service_ids(session, problem)
    candidates: list[tuple[int, SLARule]] = []
    for rule in session.execute(select(SLARule).where(SLARule.priority == problem.priority)).scalars().all():
        if rule.subsidiary and rule.subsidiary not in owning_subs:
            continue
        if rule.service_id and rule.service_id not in owning_services:
            continue
        specificity = (rule.subsidiary is not None) + (rule.service_id is not None)
        candidates.append((specificity, rule))
    if not candidates:
        return None
    return max(candidates, key=lambda pair: pair[0])[1]


async def run_sla_breaches(bot: Bot, session, domain: str) -> None:
    """Раздел «SLA», Этап 2 — открытая проблема, чей возраст превышает
    response_minutes подходящего правила, получает ОДНО напоминание за
    жизненный цикл (SlaBreachNotice — раздел И5/9.8-стиль идемпотентность,
    не спамим на каждый тик)."""
    now = datetime.utcnow()
    already = set(session.execute(select(SlaBreachNotice.problem_id)).scalars().all())
    open_problems = session.execute(
        select(Problem).where(Problem.status.in_(("OPEN", "FLAPPING")))
    ).scalars().all()
    for problem in open_problems:
        if problem.id in already:
            continue
        rule = _matching_sla_rule(session, problem)
        if rule is None:
            continue
        age_minutes = int((now - problem.opened_at).total_seconds() // 60)
        if age_minutes < rule.response_minutes:
            continue

        notice = SlaBreachNotice(problem_id=problem.id, sla_rule_id=rule.id, created_at=now)
        session.add(notice)
        session.commit()  # раздел 9.8 п.1 — помечено ДО рассылки, повторный тик не найдёт эту проблему снова

        text = render_sla_breach(
            problem_id=problem.id, incident_id=problem.incident_id,
            object_name=_object_display(session, problem.object_id), priority=problem.priority,
            age_minutes=age_minutes, threshold_minutes=rule.response_minutes, rule_name=rule.name,
        )
        for username in resolve_recipients(session, problem):
            try:
                chat_id = await get_chat_id(bot, username, domain)
                notification = Notification(problem_id=problem.id, type="SLA_BREACH", chat_id=chat_id,
                                             status="queued", created_at=datetime.utcnow())
                session.add(notification)
                session.commit()
                resp = await bot.send_message(chat_id=chat_id, text=text, parse_mode=ParseMode.HTML)
                notification.message_id = resp.message_id
                notification.status = "sent"
                notification.sent_at = datetime.utcnow()
                session.commit()
            except Exception as exc:  # noqa: BLE001
                print(f"delivery_trueconf: ошибка SLA_BREACH для problem={problem.id} "
                      f"recipient={username}: {exc}")


_chat_cache: dict[str, str] = {}


async def get_chat_id(bot: Bot, username: str, domain: str) -> str:
    """Личный чат на подписчика создаётся лениво и кэшируется на время
    жизни процесса — TrueConf не выдаёт chat_id заранее, только через
    create_personal_chat(), а повторный вызов для того же пользователя
    просто возвращает уже существующий личный чат (операция идемпотентна,
    кэш здесь только чтобы не дёргать сеть на каждый тик)."""
    if username not in _chat_cache:
        chat = await bot.create_personal_chat(user_id=f"{username}@{domain}")
        _chat_cache[username] = chat.chat_id
    return _chat_cache[username]


async def delivery_tick(bot: Bot, domain: str) -> None:
    with get_session() as session:
        open_problems = session.execute(
            select(Problem).where(Problem.status.in_(("OPEN", "FLAPPING")))
        ).scalars().all()
        for problem in open_problems:
            if problem.duplicate_of_problem_id is not None:
                # раздел 4.1 — ИИ определила дубль (packages/ai/dedup.py):
                # не полноценный NEW своим подписчикам, а короткая пометка
                # в чаты, уже получившие оригинал.
                try:
                    await notify_duplicate(bot, session, problem)
                except Exception as exc:  # noqa: BLE001
                    print(f"delivery_trueconf: ошибка DUPLICATE_NOTE для problem={problem.id}: {exc}")
                continue

            recipients = resolve_recipients(session, problem)
            if not recipients:
                continue  # раздел 4.2: у объекта/сервиса может не быть подписчика — это не ошибка
            already_chat_ids = set(session.execute(
                select(Notification.chat_id).where(Notification.problem_id == problem.id,
                                                     Notification.type == "NEW")
            ).scalars().all())
            for username in recipients:
                try:
                    chat_id = await get_chat_id(bot, username, domain)
                    if chat_id in already_chat_ids:
                        continue  # раздел 9.8 п.2 — идемпотентность per-получатель
                    await notify_new(bot, session, problem, chat_id, username)
                    already_chat_ids.add(chat_id)
                except Exception as exc:  # noqa: BLE001
                    print(f"delivery_trueconf: ошибка NEW для problem={problem.id} "
                          f"recipient={username}: {exc}")

        resolved_with_new = session.execute(
            select(Problem, Notification)
            .join(Notification, (Notification.problem_id == Problem.id) & (Notification.type == "NEW"))
            .where(Problem.status == "RESOLVED", Notification.status == "sent")
        ).all()
        for problem, new_notification in resolved_with_new:
            already_closed = session.execute(
                select(Notification.id).where(Notification.problem_id == problem.id,
                                               Notification.type == "CLOSURE",
                                               Notification.chat_id == new_notification.chat_id)
            ).scalar_one_or_none()
            if already_closed:
                continue
            try:
                await notify_closure(bot, session, problem, new_notification)
            except Exception as exc:  # noqa: BLE001
                print(f"delivery_trueconf: ошибка CLOSURE для problem={problem.id} "
                      f"chat={new_notification.chat_id}: {exc}")

        # SUPPLEMENT с ИИ-сводкой (раздел 13) — запускается в фоне (см.
        # docstring send_supplement), чтобы холодный старт LLM не задерживал
        # обычные NEW/CLOSURE следующего тика.
        for incident_id, root_problem_id, notification_id, sup_chat_id, reply_id in \
                claim_pending_supplements(session):
            asyncio.create_task(send_supplement(
                bot, incident_id=incident_id, root_problem_id=root_problem_id,
                notification_id=notification_id, chat_id=sup_chat_id, reply_message_id=reply_id,
            ))

        # Раздел «Сценарии»/«SLA», Этап 2 — синхронно (без LLM, фоновая
        # задача не нужна, см. docstring run_scenarios).
        await run_scenarios(bot, session, domain)
        await run_sla_breaches(bot, session, domain)


async def delivery_loop(bot: Bot, domain: str) -> None:
    while True:
        await delivery_tick(bot, domain)
        await asyncio.sleep(POLL_INTERVAL_S)


def ensure_fallback_subscriber(session) -> None:
    """Если в базе ещё нет ни одного подписчика (свежий стенд/после
    миграции схемы), заводим TRUECONF_TEST_RECIPIENT с подпиской "вижу
    всё" — иначе self-service-платформа на старте молчит для всех, что
    хуже, чем поведение до M5. Как только появляется хотя бы один
    подписчик (в т.ч. этот же fallback, если его отредактировали через
    личный кабинет), повторный запуск ничего не трогает."""
    if session.execute(select(Subscriber.id).limit(1)).scalar_one_or_none() is not None:
        return
    subscriber = Subscriber(trueconf_username=TRUECONF_TEST_RECIPIENT,
                             display_name="Тестовый получатель (по умолчанию)",
                             access_token=secrets.token_urlsafe(24),
                             created_at=datetime.utcnow())
    session.add(subscriber)
    session.flush()
    session.add(Subscription(subscriber_id=subscriber.id, subsidiary=None, service_id=None,
                              priority_threshold=None, created_at=datetime.utcnow()))
    session.commit()
    print(f"delivery_trueconf: fallback-подписчик {TRUECONF_TEST_RECIPIENT} создан, "
          f"личный кабинет: {PUBLIC_CONSOLE_URL}/me/{TRUECONF_TEST_RECIPIENT}/?token={subscriber.access_token}")


async def main():
    Base.metadata.create_all(bind=engine)
    dp = Dispatcher()
    dp.include_router(router)

    ssl_context = _build_ssl_context(True)
    token = _get_auth_token(
        TRUECONF_SERVER, TRUECONF_BOT_USERNAME, TRUECONF_BOT_PASSWORD, ssl_context=ssl_context,
        protocol="https" if TRUECONF_HTTPS else "http", port=TRUECONF_PORT,
    )
    if not token:
        raise RuntimeError(f"Не удалось получить токен для {TRUECONF_BOT_USERNAME}@{TRUECONF_SERVER}")

    bot = Bot(
        TRUECONF_SERVER, token, https=TRUECONF_HTTPS, web_port=TRUECONF_PORT,
        verify_ssl=ssl_context, dispatcher=dp, on_health_check=on_health_check,
    )

    run_task = asyncio.create_task(bot.run())
    await asyncio.wait_for(bot.authorized_event.wait(), timeout=30)

    # bot.create_personal_chat() сам дописывает домен через self.server_name,
    # а тот резолвится HTTP-запросом, который у нас не проходит (сервер без
    # HTTPS/443) — получаем "None" вместо домена и чат уходит "user@None",
    # то есть НЕ настоящему пользователю. Берём домен из собственного JID
    # бота (bot.me_id = "dispatcher_bot@<домен>"), который уже известен
    # после авторизации, и дописываем его сами (см. get_chat_id).
    domain = bot.me_id.split("@", 1)[1] if "@" in bot.me_id else None
    if not domain:
        raise RuntimeError(f"Не удалось определить домен из JID бота: {bot.me_id!r}")

    with get_session() as session:
        ensure_fallback_subscriber(session)

    print(f"delivery_trueconf: подключён как {bot.me_id}, домен={domain}, "
          f"адресация — по подпискам (раздел 8)")

    await asyncio.gather(run_task, delivery_loop(bot, domain))


if __name__ == "__main__":
    asyncio.run(main())
