"""Раздел IX доп. ТЗ: 4 сценария, построенных на реальном демо-штате
(scripts/seed_employees.py) — не абстрактные "получатель 1/2", а именно
Иванов/Петров/Волков/Смирнов/Орлов, чьи availability-интервалы уже
выставлены сидированием сотрудников (Иванов в отпуске, Волков на
больничном). Каждый шаблон — реальный граф через Go planner
(scenario_runs/scenario_run_steps/notifications/delivery_outbox), не
JavaScript-анимация и не описание словами.

Сценарий E (незакрытый инцидент) не материализуется как отдельный
шаблон — это не свойство графа, а условие демонстрации: отправить
alert и НЕ отправлять вслед resolved-событие, что уже проверяется
живыми SQL-запросами (см. COMPLIANCE_MATRIX.md).

Пример:
    python3 scripts/seed_demo_scenarios.py \
        --url https://xn--80aebrvrg.xn--p1acf/console --username admin1 --password admin123 --activate
"""
from __future__ import annotations

import argparse
import json
import sys

import httpx


def login(client: httpx.Client, username: str, password: str) -> None:
    client.post("/api/auth/login", json={"username": username, "password": password}).raise_for_status()


def employee_ids(client: httpx.Client, usernames: list[str]) -> dict[str, int]:
    employees = client.get("/api/employees").raise_for_status().json()
    by_username = {e["trueconf_username"]: e["id"] for e in employees}
    missing = [u for u in usernames if u not in by_username]
    if missing:
        print(f"отсутствуют сотрудники (прогоните seed_employees.py сначала): {missing}", file=sys.stderr)
        sys.exit(1)
    return {u: by_username[u] for u in usernames}


def node(node_id: str, kind: str, x: int, y: int, **data) -> dict:
    return {"id": node_id, "type": kind, "position": {"x": x, "y": y}, "data": {"kind": kind, **data}}


def edge(source: str, target: str, handle: str | None = None) -> dict:
    return {"source": source, "target": target, "sourceHandle": handle}


def build_templates(ids: dict[str, int]) -> list[dict]:
    return [
        {
            "name": "Демо A — дежурный доступен",
            "description": (
                "Раздел IX.38 доп. ТЗ: P1 -> ответственный (Смирнов) доступен -> "
                "TrueConf -> ACK. Реальный человек из демо-штата, не абстракция."
            ),
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, priority_min="P1"),
                    node("notify-1", "notify", 40, 160, employee_id=ids["smirnov.aa"], employee_label="Смирнов Артём Андреевич"),
                    node("wait-1", "wait", 40, 280, minutes=2),
                    node("ack-1", "ack_check", 40, 400),
                ],
                "edges": [
                    edge("cond-1", "notify-1"),
                    edge("notify-1", "wait-1"),
                    edge("wait-1", "ack-1"),
                ],
            }),
        },
        {
            "name": "Демо B — отпуск основного, уведомляется резерв",
            "description": (
                "Раздел IX.39: P0 -> основной (Иванов, в отпуске по демо-сидированию) "
                "исключён -> резерв (Петров, на дежурстве) реально получает TrueConf. "
                "first_available резолвит availability.Resolve по факту, не по сценарию."
            ),
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, priority_min="P0"),
                    node("notify-1", "notify", 40, 160, recipient_mode="first_available",
                         employee_ids=[ids["ivanov.an"], ids["petrov.ns"]],
                         employee_labels=["Иванов Алексей Николаевич", "Петров Николай Сергеевич"]),
                    node("notify-none", "notify", 260, 300, employee_id=ids["orlov.d"], employee_label="Орлов Дмитрий Викторович"),
                ],
                "edges": [
                    edge("cond-1", "notify-1"),
                    edge("notify-1", "notify-none", "no_recipient"),
                ],
            }),
        },
        {
            "name": "Демо C — больничный + эскалация руководителю смены",
            "description": (
                "Раздел IX.40: P1 Network -> основной (Волков, на больничном по демо-"
                "сидированию) исключён -> Смирнов получает TrueConf -> нет ACK 5 минут "
                "(в демо — 2, ради разумного времени показа) -> эскалация Орлову."
            ),
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, symptom_class="interface_down"),
                    node("notify-1", "notify", 40, 160, recipient_mode="first_available",
                         employee_ids=[ids["volkov.mv"], ids["smirnov.aa"]],
                         employee_labels=["Волков Максим Викторович", "Смирнов Артём Андреевич"]),
                    node("wait-1", "wait", 40, 280, minutes=2),
                    node("ack-1", "ack_check", 40, 400),
                    node("notify-2", "notify", 260, 520, employee_id=ids["orlov.d"], employee_label="Орлов Дмитрий Викторович"),
                ],
                "edges": [
                    edge("cond-1", "notify-1"),
                    edge("notify-1", "wait-1"),
                    edge("wait-1", "ack-1"),
                    edge("ack-1", "notify-2", "no"),
                ],
            }),
        },
        {
            "name": "Демо D — параллельная доставка TrueConf + Email",
            "description": (
                "Раздел IX.41 / VII.29: P0 -> уведомление уходит одновременно в "
                "TrueConf и на почту (Титов, email_enabled=true по демо-сидированию) "
                "— через production delivery-контракт (channel), не имитация."
            ),
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, priority_min="P0", symptom_class="power_lost"),
                    node("notify-1", "notify", 40, 160, employee_id=ids["titov.dv"],
                         employee_label="Титов Денис Викторович", channel="both"),
                ],
                "edges": [edge("cond-1", "notify-1")],
            }),
        },
    ]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    parser.add_argument("--username", required=True)
    parser.add_argument("--password", required=True)
    parser.add_argument("--activate", action="store_true")
    args = parser.parse_args()

    with httpx.Client(base_url=args.url, timeout=30) as client:
        login(client, args.username, args.password)
        needed = ["smirnov.aa", "ivanov.an", "petrov.ns", "orlov.d", "volkov.mv", "titov.dv"]
        ids = employee_ids(client, needed)
        templates = build_templates(ids)

        existing = {s["name"]: s["id"] for s in client.get("/api/scenarios").raise_for_status().json()}
        for template in templates:
            if template["name"] in existing:
                print(f"пропуск (уже есть): {template['name']} (id={existing[template['name']]})")
                continue
            created = client.post("/api/scenarios", json={
                "name": template["name"], "description": template["description"], "graph_json": template["graph_json"],
            })
            created.raise_for_status()
            scenario_id = created.json()["id"]
            print(f"создан: {template['name']} (id={scenario_id})")
            if args.activate:
                activation = client.post(f"/api/scenarios/{scenario_id}/activate")
                if activation.status_code >= 400:
                    print(f"  предупреждение: не удалось активировать: {activation.text}", file=sys.stderr)
                else:
                    print("  активирован")


if __name__ == "__main__":
    main()
