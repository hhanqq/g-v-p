"""Тесты шаблонов уведомлений — раздел 9.3. Ключевая проверка — И2:
оригинал источника передаётся дословно, шаблонизация его не касается."""
from datetime import datetime

from services.delivery_trueconf.templates import (display_id, format_duration,
                                                    render_closure, render_daily_summary,
                                                    render_duplicate_note, render_new,
                                                    render_supplement)


def test_original_body_passed_verbatim():
    original = "PROBLEM: Well-4 PLC unreachable\nHost: well4-plc-01 (10.42.8.17)\nSeverity: Disaster"
    text = render_new(problem_id=1, incident_id=42, priority="P0", object_name="well4-plc-01",
                       site_name="БРД-Ноябрьск", service_name="АСУ ТП куста №4",
                       source_system="zabbix", original_body=original, symptom_class="host_unreachable")
    assert original in text
    # И2: не перефразировано — ищем оригинал ЦЕЛИКОМ, без изменений построчно
    for line in original.split("\n"):
        assert line in text


def test_render_new_includes_alerts_link_when_provided():
    text = render_new(problem_id=1, incident_id=None, priority="P1", object_name="x", site_name="y",
                       service_name=None, source_system="zabbix", original_body="body",
                       symptom_class="host_unreachable", alerts_link="https://example/alerts/u/?token=t")
    assert "https://example/alerts/u/?token=t" in text


def test_render_new_omits_alerts_link_when_not_provided():
    text = render_new(problem_id=1, incident_id=None, priority="P1", object_name="x", site_name="y",
                       service_name=None, source_system="zabbix", original_body="body",
                       symptom_class="host_unreachable")
    assert "текущие алерты" not in text


def test_display_id_uses_incident_when_present():
    assert display_id(7, 42) == "INC-0042"
    assert display_id(7, None) == "PRB-0007"


def test_priority_emoji_present_for_all_levels():
    for priority in ["P0", "P1", "P2", "P3"]:
        text = render_new(problem_id=1, incident_id=None, priority=priority, object_name="x",
                           site_name="y", service_name=None, source_system="zabbix",
                           original_body="body", symptom_class="host_unreachable")
        assert priority in text

    # приоритет неизвестен/не посчитан — сообщение всё равно уходит (И5), не падаем
    text = render_new(problem_id=1, incident_id=None, priority=None, object_name="x",
                       site_name="y", service_name=None, source_system="zabbix",
                       original_body="body", symptom_class="host_unreachable")
    assert "PRB-0001" in text


def test_render_closure_notes_reconciliation():
    resolved_at = datetime(2026, 8, 6, 3, 41, 8)
    text = resolved = render_closure(problem_id=1, incident_id=None, resolved_at=resolved_at,
                                      duration_text="26 мин", closed_by_reconciliation=True)
    assert "автоматически по таймауту" in text
    text2 = render_closure(problem_id=1, incident_id=None, resolved_at=resolved_at,
                            duration_text="26 мин", closed_by_reconciliation=False)
    assert "автоматически по таймауту" not in text2


def test_render_daily_summary_empty_day_skips_ai_and_counts():
    text = render_daily_summary(date_str="2026-08-08", total=0, open_count=0, resolved_count=0,
                                 by_priority={}, top_symptoms=[], ai_text=None)
    assert "не адресовано ни одного алерта" in text
    assert "гипотеза" not in text


def test_render_daily_summary_includes_facts_and_optional_ai_paragraph():
    text = render_daily_summary(date_str="2026-08-08", total=7, open_count=2, resolved_count=5,
                                 by_priority={"P0": 1, "P1": 3, "P2": 3},
                                 top_symptoms=[("host_unreachable", 4), ("disk_space", 2)], ai_text=None)
    assert "Адресовано вам: 7" in text
    assert "открыто сейчас: 2" in text
    assert "P0: 1" in text
    assert "host_unreachable (4)" in text
    assert "гипотеза" not in text  # ai_text не передан — раздел И5, абзаца просто нет

    with_ai = render_daily_summary(date_str="2026-08-08", total=7, open_count=2, resolved_count=5,
                                    by_priority={"P0": 1}, top_symptoms=[("host_unreachable", 4)],
                                    ai_text="Сегодня преобладали отказы связи.")
    assert "Сегодня преобладали отказы связи." in with_ai
    assert "гипотеза" in with_ai


def test_format_duration_units():
    assert format_duration(45) == "45 с"
    assert format_duration(125) == "2 мин 5 с"
    assert format_duration(3725) == "1 ч 2 мин"


def test_supplement_marks_ai_summary_as_hypothesis_below_facts():
    opened = datetime(2026, 8, 6, 11, 0, 1)
    text = render_supplement(problem_id=1, incident_id=7, root_object="sw-acc-04",
                              root_symptom_class="node_down", opened_at=opened, n_symptoms=3,
                              n_services=2, rule_names=["corr-114"], ai_summary="Коммутатор упал.")
    facts_idx = text.index("Основание")
    hypothesis_idx = text.index("гипотеза")
    assert facts_idx < hypothesis_idx  # факты правила выше сводки ИИ
    assert "Коммутатор упал." in text
    assert "INC-0007" in text


def test_supplement_without_ai_summary_still_has_facts():
    text = render_supplement(problem_id=1, incident_id=7, root_object="sw-acc-04",
                              root_symptom_class="node_down", opened_at=datetime(2026, 8, 6),
                              n_symptoms=3, n_services=2, rule_names=[], ai_summary=None)
    assert "не определено" in text
    assert "гипотеза" not in text  # раздел 13: отказ LLM не должен ронять доставку факт-части


def test_duplicate_note_references_original_and_source():
    text = render_duplicate_note(duplicate_problem_id=42, original_problem_id=10,
                                  original_incident_id=7, source_system="solarwinds")
    assert "PRB-0042" in text
    assert "INC-0007" in text  # оригинал показан по инциденту, не по номеру проблемы
    assert "solarwinds" in text
    assert "ДУБЛЬ" in text


def test_duplicate_note_falls_back_to_problem_id_without_incident():
    text = render_duplicate_note(duplicate_problem_id=42, original_problem_id=10,
                                  original_incident_id=None, source_system="zabbix")
    assert "PRB-0010" in text


def test_new_without_hypothesis_has_no_hypothesis_section():
    text = render_new(problem_id=1, incident_id=None, priority="P1", object_name="sw-01",
                       site_name="БРД-Ноябрьск", service_name=None, source_system="zabbix",
                       original_body="PROBLEM: x", symptom_class="node_down")
    assert "гипотеза" not in text


def test_new_with_hypothesis_appends_labeled_section_after_facts():
    text = render_new(problem_id=1, incident_id=None, priority="P0", object_name="sw-01",
                       site_name="БРД-Ноябрьск", service_name=None, source_system="solarwinds",
                       original_body="PROBLEM: x", symptom_class="node_down",
                       ai_root_cause_hypothesis="Скорее всего, причина в питании.")
    assert "гипотеза" in text
    assert "Скорее всего, причина в питании." in text
    assert text.index("Симптом") < text.index("гипотеза")  # факты выше предположения
