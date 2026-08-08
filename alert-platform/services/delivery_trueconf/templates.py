"""Шаблоны уведомлений — раздел 9.3: конверт, а не письмо. Тело — дословный
оригинал источника (И2), не проходит через шаблонизацию. Шапка и подвал —
шаблон.

Сознательно урезано относительно раздела 9.3 для M4: строка действий
("Ответьте: взял · не мой · следствие") не включена — полная семантика
раздела 9.4 не реализована (см. packages/common/ack.py — только бинарный
факт реакции, минимальный срез). Ссылка в личный кабинет и на текущие
алерты (Этап 4) — реализованы, включаются в render_new ниже.
"""
from __future__ import annotations

from datetime import datetime

PRIORITY_EMOJI = {"P0": "🔴", "P1": "🟠", "P2": "🟡", "P3": "⚪"}


def display_id(problem_id: int, incident_id: int | None) -> str:
    return f"INC-{incident_id:04d}" if incident_id else f"PRB-{problem_id:04d}"


def render_new(*, problem_id: int, incident_id: int | None, priority: str | None,
                object_name: str, site_name: str, service_name: str | None,
                source_system: str, original_body: str, symptom_class: str,
                ai_root_cause_hypothesis: str | None = None, alerts_link: str | None = None) -> str:
    emoji = PRIORITY_EMOJI.get(priority or "P3", "⚪")
    did = display_id(problem_id, incident_id)
    service_line = f"Сервис: {service_name}" if service_name else "Сервис: не определён"
    text = (
        f"{emoji} <b>{priority or '?'} · {did}</b> · {object_name} · {site_name}\n"
        f"─────── оригинал {source_system} ───────\n"
        f"{original_body}\n"
        f"───────────────────────────────\n"
        f"{service_line} · Симптом: {symptom_class}"
    )
    if ai_root_cause_hypothesis:
        # раздел 6.1/13 — коррелятор не связал событие правилом (например,
        # раздел 18.4 site_outage_ambiguous), ИИ-гипотеза дополняет, но не
        # заменяет факты выше; явно маркирована как предположение.
        text += (f"\n\n<i>Вероятная первопричина (гипотеза, требует проверки, "
                 f"сформирована ИИ):</i>\n{ai_root_cause_hypothesis}")
    if alerts_link:
        # Этап 4 — сотрудник должен суметь одним тапом из самого алерта
        # увидеть полную текущую картину и последовательность (не только
        # этот один алерт), не набирая команду /алерты вручную.
        text += f"\n\nВаши текущие алерты и последовательность: {alerts_link}"
    return text


def render_closure(*, problem_id: int, incident_id: int | None,
                    resolved_at: datetime, duration_text: str,
                    closed_by_reconciliation: bool) -> str:
    did = display_id(problem_id, incident_id)
    note = " (закрыто автоматически по таймауту, без сообщения о восстановлении)" \
        if closed_by_reconciliation else ""
    return (
        f"🟢 <b>ЗАКРЫТО · {did}</b>\n"
        f"Восстановлено {resolved_at:%Y-%m-%d %H:%M:%S} · Длительность {duration_text}{note}"
    )


def render_supplement(*, problem_id: int, incident_id: int, root_object: str,
                       root_symptom_class: str, opened_at: datetime, n_symptoms: int,
                       n_services: int, rule_names: list[str], ai_summary: str | None,
                       ai_recommendation: str | None = None,
                       checklist_fallback: list[str] | None = None) -> str:
    did = display_id(problem_id, incident_id)
    rules = ", ".join(rule_names) or "не определено"
    lines = [
        f"🔵 <b>ДОПОЛНЕНИЕ к {did}</b>",
        f"Первопричина: {root_object} ({root_symptom_class}), с {opened_at:%Y-%m-%d %H:%M:%S}",
        f"Связано алертов: {n_symptoms} · Затронуто сервисов: {n_services}",
        f"Основание: правило {rules}",
    ]
    if ai_summary:
        # раздел 13: вывод модели помечен как гипотеза, ниже фактов правила.
        lines.append("")
        lines.append("<i>Сводка (гипотеза, сформирована ИИ):</i>")
        lines.append(ai_summary)
    if ai_recommendation:
        # ИИ выбрала и сформулировала пункты из чек-листа (packages/ai/recommend.py)
        # под конкретику инцидента — сами пункты не выдуманы, только фразировка/отбор.
        lines.append("")
        lines.append("<i>Рекомендация (на основе базы знаний, сформулирована ИИ):</i>")
        lines.append(ai_recommendation)
    elif checklist_fallback:
        # раздел И5: ИИ недоступна — показываем чек-лист как есть, без фразировки.
        lines.append("")
        lines.append("<i>Рекомендация (чек-лист из базы знаний):</i>")
        lines.extend(f"• {step}" for step in checklist_fallback)
    return "\n".join(lines)


def render_duplicate_note(*, duplicate_problem_id: int, original_problem_id: int,
                           original_incident_id: int | None, source_system: str) -> str:
    """Раздел 4.1 — ИИ определила, что это тот же реальный объект,
    описанный другим источником другими словами (packages/ai/dedup.py).
    Короткая пометка вместо ещё одного полноценного NEW — раздел И2/И3
    не нарушены: оригинал всё ещё доступен по PRB-id для трассировки."""
    did = display_id(original_problem_id, original_incident_id)
    return (
        f"🔗 <b>ДУБЛЬ</b> · PRB-{duplicate_problem_id:04d}\n"
        f"Похоже на то же событие, что и {did} — подтверждение от {source_system} "
        f"(определено ИИ, раздел 4.1). Отдельное уведомление не отправлено."
    )


def render_scenario_notify(*, problem_id: int, incident_id: int | None, scenario_name: str,
                            object_name: str, is_escalation: bool) -> str:
    """Раздел «Сценарии», Этап 2 — уведомление по шагу линейной цепочки
    (packages/scenarios/engine.py). is_escalation=True — это шаг ПОСЛЕ
    «Подождать» (дедлайн истёк раньше, чем проблема решилась), не первое
    уведомление сценария."""
    did = display_id(problem_id, incident_id)
    kind = "ЭСКАЛАЦИЯ ПО СЦЕНАРИЮ" if is_escalation else "СЦЕНАРИЙ"
    return (
        f"🟣 <b>{kind}: {scenario_name}</b> · {did}\n"
        f"Объект: {object_name}"
    )


def render_sla_breach(*, problem_id: int, incident_id: int | None, object_name: str,
                       priority: str | None, age_minutes: int, threshold_minutes: int,
                       rule_name: str) -> str:
    """Раздел «SLA», Этап 2 — напоминание, что проблема в работе дольше
    норматива реагирования правила. Факты (сколько прошло, какой порог),
    не домысел — тот же принцип, что и в остальных шаблонах файла."""
    did = display_id(problem_id, incident_id)
    return (
        f"⏰ <b>НАРУШЕН SLA · {did}</b>\n"
        f"{object_name} · приоритет {priority or '?'}\n"
        f"В работе {age_minutes} мин · порог реагирования по правилу «{rule_name}» — {threshold_minutes} мин"
    )


def format_duration(seconds: float) -> str:
    seconds = int(seconds)
    h, rem = divmod(seconds, 3600)
    m, s = divmod(rem, 60)
    if h:
        return f"{h} ч {m} мин"
    if m:
        return f"{m} мин {s} с"
    return f"{s} с"
