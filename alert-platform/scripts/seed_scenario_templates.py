"""Засевает 5 демо-шаблонов сценариев (раздел «Управление реакцией и
доступностью») через реальный Go admin API — не миграцию: миграции
это схема, не seed-данные (ни одна из 0000-0016 не сеет строки), а
граф сценария должен оставаться редактируемым JSON, не запечённым в
SQL.

Идемпотентен: пропускает шаблон, если сценарий с таким именем уже
существует. Самодостаточен по группам/сотрудникам — если демо-группы
"Механики Ноябрьск"/"НОК L1" ещё не созданы (свежий стенд без
предварительной настройки), создаёт их и добавляет в участники первых
найденных активных сотрудников (тот же принцип отказоустойчивости, что
у ensure_fallback_subscriber в services/delivery_trueconf).

Пример:
    python3 scripts/seed_scenario_templates.py \
        --url https://xn--80aebrvrg.xn--p1acf/console --username admin1 --password admin123
"""
from __future__ import annotations

import argparse
import json
import sys

import httpx

PRIMARY_GROUP = "Механики Ноябрьск"
ESCALATION_GROUP = "НОК L1"


def login(client: httpx.Client, username: str, password: str) -> None:
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    resp.raise_for_status()


def ensure_group(client: httpx.Client, name: str, member_ids: list[int]) -> int:
    groups = client.get("/api/groups").raise_for_status().json()
    existing = next((g for g in groups if g["name"] == name), None)
    if existing:
        return existing["id"]
    created = client.post("/api/groups", json={"name": name, "description": "демо-группа (seed_scenario_templates.py)"})
    created.raise_for_status()
    group_id = created.json()["id"]
    for subscriber_id in member_ids:
        client.post(f"/api/groups/{group_id}/members", json={"subscriber_id": subscriber_id})
    return group_id


def ensure_demo_actors(client: httpx.Client) -> tuple[int, int, list[int]]:
    """Возвращает (id группы дежурных, id группы эскалации, [employee_ids для явного списка])."""
    employees = client.get("/api/employees").raise_for_status().json()
    active_ids = [e["id"] for e in employees if e.get("active")]
    if len(active_ids) < 2:
        print("Недостаточно активных сотрудников (нужно хотя бы 2) — пропускаю засев шаблонов.", file=sys.stderr)
        sys.exit(1)
    primary_id = ensure_group(client, PRIMARY_GROUP, active_ids[:2])
    escalation_id = ensure_group(client, ESCALATION_GROUP, active_ids[-1:])
    return primary_id, escalation_id, active_ids[:2]


def node(node_id: str, kind: str, x: int, y: int, **data) -> dict:
    return {"id": node_id, "type": kind, "position": {"x": x, "y": y}, "data": {"kind": kind, **data}}


def edge(source: str, target: str, handle: str | None = None) -> dict:
    e = {"source": source, "target": target, "sourceHandle": handle}
    return e


def build_templates(primary_group: int, escalation_group: int, ordered_employees: list[int]) -> list[dict]:
    return [
        {
            "name": "Единый ответственный с резервом",
            "description": "Демо-шаблон: notify(all) -> [нет получателя] -> notify(руководитель смены). Показывает явную эскалацию вместо тихого пропуска (см. Фазу 0).",
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, symptom_class="node_down"),
                    node("notify-1", "notify", 40, 160, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("notify-2", "notify", 260, 300, group_id=escalation_group, group_label=ESCALATION_GROUP),
                ],
                "edges": [
                    edge("cond-1", "notify-1"),
                    edge("notify-1", "notify-2", "no_recipient"),
                ],
            }),
        },
        {
            "name": "Дежурная группа с проверкой доступности",
            "description": "Демо-шаблон: availability_check(группа) -> да: notify(группа, первый доступный) / нет: notify(руководитель смены).",
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, symptom_class="disk_space"),
                    node("avail-1", "availability_check", 40, 160, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("notify-yes", "notify", -120, 300, group_id=primary_group, group_label=PRIMARY_GROUP, recipient_mode="first_available"),
                    node("notify-no", "notify", 200, 300, group_id=escalation_group, group_label=ESCALATION_GROUP),
                ],
                "edges": [
                    edge("cond-1", "avail-1"),
                    edge("avail-1", "notify-yes", "yes"),
                    edge("avail-1", "notify-no", "no"),
                ],
            }),
        },
        {
            "name": "Эскалация по недоступности первого дежурного",
            "description": "Демо-шаблон: notify(упорядоченный список, первый доступный) -> подождать -> проверка реакции -> нет: notify(второй в списке).",
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, symptom_class="service_down"),
                    node("notify-1", "notify", 40, 160, recipient_mode="first_available", employee_ids=ordered_employees),
                    node("wait-1", "wait", 40, 280, minutes=10),
                    node("ack-1", "ack_check", 40, 400),
                    node("notify-2", "notify", 260, 520, employee_id=ordered_employees[-1]),
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
            "name": "P0 без доступных дежурных — руководитель смены",
            "description": "Демо-шаблон: условие(P0) -> проверка доступности дежурной группы -> нет: notify(руководитель смены) сразу, без ожидания.",
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, priority_min="P0"),
                    node("avail-1", "availability_check", 40, 160, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("notify-yes", "notify", -120, 300, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("notify-no", "notify", 200, 300, group_id=escalation_group, group_label=ESCALATION_GROUP),
                ],
                "edges": [
                    edge("cond-1", "avail-1"),
                    edge("avail-1", "notify-yes", "yes"),
                    edge("avail-1", "notify-no", "no"),
                ],
            }),
        },
        {
            "name": "Дежурство по подписке и доступности",
            "description": "Демо-шаблон: проверка подписки -> да: проверка доступности -> да: notify. Комбинирует два типа проверки.",
            "graph_json": json.dumps({
                "nodes": [
                    node("cond-1", "condition", 40, 40, symptom_class="interface_down"),
                    node("sub-1", "subscription_check", 40, 160, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("avail-1", "availability_check", 40, 280, group_id=primary_group, group_label=PRIMARY_GROUP),
                    node("notify-1", "notify", 40, 400, group_id=primary_group, group_label=PRIMARY_GROUP, recipient_mode="first_available"),
                ],
                "edges": [
                    edge("cond-1", "sub-1"),
                    edge("sub-1", "avail-1", "yes"),
                    edge("avail-1", "notify-1", "yes"),
                ],
            }),
        },
    ]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True, help="Базовый URL admin-console, например https://.../console")
    parser.add_argument("--username", required=True)
    parser.add_argument("--password", required=True)
    parser.add_argument("--activate", action="store_true", help="Сразу активировать каждый созданный шаблон")
    args = parser.parse_args()

    with httpx.Client(base_url=args.url, timeout=30) as client:
        login(client, args.username, args.password)
        primary_group, escalation_group, ordered_employees = ensure_demo_actors(client)
        templates = build_templates(primary_group, escalation_group, ordered_employees)

        existing = {s["name"] for s in client.get("/api/scenarios").raise_for_status().json()}
        for template in templates:
            if template["name"] in existing:
                print(f"пропуск (уже есть): {template['name']}")
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
