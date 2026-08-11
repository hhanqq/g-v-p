"""Демо-штат (раздел VIII доп. ТЗ): 27 реалистичных сотрудников вместо
2-3 технических тест-аккаунтов — роли, компетенции, зоны ответственности
(группы + group_equipment_scope), пересекающаяся ответственность
(основной/резерв на каждую категорию оборудования каждого филиала) и
уже выставленные интервалы доступности (отпуск/больничный/дежурство/
делегирование), дающие сценариям реальные разные ветки на разных людях
прямо после сидирования — не только "все всегда доступны".

Также строит дерево организации (org_units, раздел «Сотрудники» доп. ТЗ:
дерево вместо плоской карточной сетки) над теми же реальными данными —
не выдуманная отдельная таксономия, а два представления (группы для
маршрутизации, дерево для навигации) над одним и тем же составом
филиалов/ролей: Организация → Филиал → Отдел (АСУ ТП/Сеть/Серверы) →
Сотрудник для инженеров (4 уровня) и Организация → Руководство →
Сотрудник для руководителей (2 уровня) — глубина не одинаковая
специально, чтобы дерево не выглядело как решение с фиксированным
числом уровней.

Через реальный Go admin API (как и seed_scenario_templates.py) — не
миграцию: сотрудники, группы и org_units это данные, не схема. Идемпотентен:
пропускает сотрудника/группу/org_unit, если trueconf_username/название
уже есть. LDAP-аккаунты (тот же trueconf_username) — ldap/glauth.cfg,
заведены отдельно и заранее: без них deprovision-worker деактивировал бы
этих сотрудников на первой сверке (см. комментарий там же).

Пример:
    python3 scripts/seed_employees.py \
        --url https://xn--80aebrvrg.xn--p1acf/console --username admin1 --password admin123
"""
from __future__ import annotations

import argparse
import sys
from datetime import datetime, timedelta

import httpx

SITES = {
    "brd-noyabrsk": "Ноябрьский филиал",
    "brd-khantos": "Ханты-Мансийский филиал",
    "brd-sakhalin": "Сахалинский филиал",
    "brd-zapolyarye": "Заполярный филиал",
}

CATEGORY_LABEL = {"plc": "АСУ ТП", "network": "Сеть", "server": "Серверы"}

# (username, full_name, position, site, equipment_type, competencies, email_enabled)
ENGINEERS = [
    ("ivanov.an", "Иванов Алексей Николаевич", "Ведущий инженер АСУ ТП", "brd-noyabrsk", "plc", "PLC, SCADA, АСУ ТП", True),
    ("petrov.ns", "Петров Николай Сергеевич", "Инженер АСУ ТП", "brd-noyabrsk", "plc", "PLC, АСУ ТП", False),
    ("volkov.mv", "Волков Максим Викторович", "Старший сетевой инженер", "brd-noyabrsk", "network", "Cisco, коммутация, мониторинг сети", False),
    ("smirnov.aa", "Смирнов Артём Андреевич", "Сетевой инженер", "brd-noyabrsk", "network", "коммутация, кабельная инфраструктура", False),
    ("kuznetsov.ii", "Кузнецов Илья Игоревич", "Linux administrator", "brd-noyabrsk", "server", "Linux, systemd, мониторинг", False),
    ("fedorov.av", "Фёдоров Андрей Валерьевич", "Windows administrator", "brd-noyabrsk", "server", "Windows Server, Active Directory", False),

    ("sidorov.pv", "Сидоров Павел Викторович", "Ведущий инженер АСУ ТП", "brd-khantos", "plc", "PLC, SCADA, АСУ ТП", True),
    ("nikitin.dm", "Никитин Дмитрий Максимович", "Инженер АСУ ТП", "brd-khantos", "plc", "PLC, АСУ ТП", False),
    ("egorov.ks", "Егоров Кирилл Станиславович", "Старший сетевой инженер", "brd-khantos", "network", "Cisco, коммутация, мониторинг сети", False),
    ("pavlov.rs", "Павлов Роман Сергеевич", "Сетевой инженер", "brd-khantos", "network", "коммутация, кабельная инфраструктура", False),
    ("sokolov.ea", "Соколов Евгений Александрович", "Linux administrator", "brd-khantos", "server", "Linux, systemd, мониторинг", False),
    ("belov.vi", "Белов Вячеслав Игоревич", "Windows administrator", "brd-khantos", "server", "Windows Server, Active Directory", False),

    ("lebedev.oa", "Лебедев Олег Артёмович", "Ведущий инженер АСУ ТП", "brd-sakhalin", "plc", "PLC, SCADA, АСУ ТП", True),
    ("kozlov.ns", "Козлов Никита Станиславович", "Инженер АСУ ТП", "brd-sakhalin", "plc", "PLC, АСУ ТП", False),
    ("novikov.ip", "Новиков Иван Павлович", "Старший сетевой инженер", "brd-sakhalin", "network", "Cisco, коммутация, мониторинг сети", False),
    ("komarov.sd", "Комаров Сергей Дмитриевич", "Сетевой инженер", "brd-sakhalin", "network", "коммутация, кабельная инфраструктура", False),
    ("zaitsev.ay", "Зайцев Артём Юрьевич", "Linux administrator", "brd-sakhalin", "server", "Linux, systemd, мониторинг", False),
    ("bykov.vn", "Быков Вадим Николаевич", "Windows administrator", "brd-sakhalin", "server", "Windows Server, Active Directory", False),

    ("titov.dv", "Титов Денис Викторович", "Ведущий инженер АСУ ТП", "brd-zapolyarye", "plc", "PLC, SCADA, АСУ ТП", True),
    ("frolov.ma", "Фролов Максим Андреевич", "Инженер АСУ ТП", "brd-zapolyarye", "plc", "PLC, АСУ ТП", False),
    ("gusev.ia", "Гусев Игорь Александрович", "Старший сетевой инженер", "brd-zapolyarye", "network", "Cisco, коммутация, мониторинг сети", False),
    ("medvedev.ov", "Медведев Олег Валерьевич", "Сетевой инженер", "brd-zapolyarye", "network", "коммутация, кабельная инфраструктура", False),
    ("vasiliev.ap", "Васильев Артём Павлович", "Linux administrator", "brd-zapolyarye", "server", "Linux, systemd, мониторинг", False),
    ("tarasov.ke", "Тарасов Кирилл Евгеньевич", "Windows administrator", "brd-zapolyarye", "server", "Windows Server, Active Directory", False),
]

# (username, full_name, position, competencies, covered_sites)
MANAGERS = [
    ("orlov.d", "Орлов Дмитрий Викторович", "Руководитель смены", "координация дежурства, эскалация", ["brd-noyabrsk", "brd-khantos"]),
    ("sokolova.ev", "Соколова Елена Викторовна", "Руководитель смены", "координация дежурства, эскалация", ["brd-sakhalin", "brd-zapolyarye"]),
    ("morozov.s", "Морозов Сергей Дмитриевич", "Руководитель эксплуатации", "эксплуатация, SLA, отчётность", []),
]

# Демо-интервалы доступности, выставленные сразу при сидировании — то,
# что раздел VIII.35 ТЗ описывает как "не должно быть слишком идеально".
# from_offset/until_offset — дни от текущего момента.
AVAILABILITY_DEMO = [
    ("ivanov.an", "vacation", 0, 7, None),
    ("volkov.mv", "sick_leave", 0, 3, None),
    ("bykov.vn", "vacation", 0, 5, None),
    ("komarov.sd", "shift", 0, 30, None),
    ("medvedev.ov", "on_call", 0, 30, None),
    ("belov.vi", "delegation", 0, 14, "sokolov.ea"),
]


def login(client: httpx.Client, username: str, password: str) -> None:
    resp = client.post("/api/auth/login", json={"username": username, "password": password})
    resp.raise_for_status()


def ensure_employee(client: httpx.Client, username: str, full_name: str, position: str,
                     competencies: str, email_enabled: bool) -> int:
    existing = client.get("/api/employees").raise_for_status().json()
    found = next((e for e in existing if e["trueconf_username"] == username), None)
    if found:
        return found["id"]
    created = client.post("/api/employees", json={
        "trueconf_username": username, "full_name": full_name, "position": position,
        "email": f"{username}@gpn-dispatcher.local",
    })
    created.raise_for_status()
    employee_id = created.json()["id"]
    client.put(f"/api/employees/{employee_id}", json={
        "competencies": competencies, "email_enabled": email_enabled, "trueconf_enabled": True,
    }).raise_for_status()
    print(f"сотрудник создан: {full_name} ({username}, id={employee_id})")
    return employee_id


ORG_ROOT_NAME = "ПАО «Газпромнефть» — Блок разведки и добычи"
LEADERSHIP_UNIT_NAME = "Руководство эксплуатации"


def get_org_tree(client: httpx.Client) -> list[dict]:
    return client.get("/api/org-units/tree").raise_for_status().json()


def ensure_org_unit(client: httpx.Client, name: str, parent: dict | None, kind: str) -> dict:
    """parent — узел дерева (с "children"), либо None для корня. Мутирует
    parent["children"] на месте, чтобы повторные вызовы в этом же запуске
    сразу видели только что созданный узел без повторного GET дерева."""
    siblings = parent["children"] if parent is not None else get_org_tree(client)
    existing = next((n for n in siblings if n["name"] == name), None)
    if existing is not None:
        return existing
    payload: dict = {"name": name, "kind": kind}
    if parent is not None:
        payload["parent_id"] = parent["id"]
    created = client.post("/api/org-units", json=payload)
    created.raise_for_status()
    node = {"id": created.json()["id"], "name": name, "kind": kind, "children": [], "employees": []}
    siblings.append(node)
    print(f"org_unit создан: {name} (id={node['id']})")
    return node


def ensure_org_structure(client: httpx.Client) -> tuple[dict[str, dict[str, dict]], dict]:
    root = ensure_org_unit(client, ORG_ROOT_NAME, None, "organization")
    site_departments: dict[str, dict[str, dict]] = {}
    for site, site_label in SITES.items():
        site_node = ensure_org_unit(client, site_label, root, "филиал")
        site_departments[site] = {
            category: ensure_org_unit(client, label, site_node, "отдел")
            for category, label in CATEGORY_LABEL.items()
        }
    leadership_node = ensure_org_unit(client, LEADERSHIP_UNIT_NAME, root, "отдел")
    return site_departments, leadership_node


def set_org_unit(client: httpx.Client, employee_id: int, org_unit_id: int) -> None:
    client.put(f"/api/employees/{employee_id}", json={"org_unit_id": org_unit_id}).raise_for_status()


def ensure_group(client: httpx.Client, name: str) -> tuple[int, bool]:
    groups = client.get("/api/groups").raise_for_status().json()
    existing = next((g for g in groups if g["name"] == name), None)
    if existing:
        return existing["id"], False
    created = client.post("/api/groups", json={"name": name, "description": "демо-штат (seed_employees.py)"})
    created.raise_for_status()
    return created.json()["id"], True


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", required=True)
    parser.add_argument("--username", required=True)
    parser.add_argument("--password", required=True)
    args = parser.parse_args()

    with httpx.Client(base_url=args.url, timeout=30) as client:
        login(client, args.username, args.password)

        usernames_to_id: dict[str, int] = {}
        site_group_id: dict[str, int] = {}
        site_departments, leadership_unit = ensure_org_structure(client)

        for site, site_label in SITES.items():
            group_id, created = ensure_group(client, f"{site_label} — дежурные")
            site_group_id[site] = group_id
            if created:
                for category in CATEGORY_LABEL:
                    client.post(f"/api/groups/{group_id}/equipment", json={"site": site, "equipment_type": category}).raise_for_status()

        for username, full_name, position, site, category, competencies, email_enabled in ENGINEERS:
            employee_id = ensure_employee(client, username, full_name, position, competencies, email_enabled)
            usernames_to_id[username] = employee_id
            members = client.get(f"/api/groups/{site_group_id[site]}").raise_for_status().json()["members"]
            if not any(m["subscriber_id"] == employee_id for m in members):
                client.post(f"/api/groups/{site_group_id[site]}/members", json={"subscriber_id": employee_id}).raise_for_status()
            set_org_unit(client, employee_id, site_departments[site][category]["id"])

        for username, full_name, position, competencies, covered_sites in MANAGERS:
            employee_id = ensure_employee(client, username, full_name, position, competencies, True)
            usernames_to_id[username] = employee_id
            group_name = f"Руководство — {full_name.split()[0]}"
            group_id, created = ensure_group(client, group_name)
            if created:
                members = []
                for site in covered_sites:
                    client.post(f"/api/groups/{group_id}/equipment", json={"site": site}).raise_for_status()
            else:
                members = client.get(f"/api/groups/{group_id}").raise_for_status().json()["members"]
            if not any(m["subscriber_id"] == employee_id for m in members):
                client.post(f"/api/groups/{group_id}/members", json={"subscriber_id": employee_id}).raise_for_status()
            set_org_unit(client, employee_id, leadership_unit["id"])

        now = datetime.utcnow()
        for username, kind, from_offset, until_offset, delegate_username in AVAILABILITY_DEMO:
            employee_id = usernames_to_id[username]
            detail = client.get(f"/api/employees/{employee_id}").raise_for_status().json()
            if any(iv["kind"] == kind for iv in detail.get("availability_history", [])):
                continue
            payload = {
                "kind": kind,
                "valid_from": (now + timedelta(days=from_offset)).strftime("%Y-%m-%dT%H:%M:%S"),
                "valid_until": (now + timedelta(days=until_offset)).strftime("%Y-%m-%dT%H:%M:%S"),
                "note": "демо-штат (seed_employees.py)",
            }
            if delegate_username:
                payload["delegate_to_subscriber_id"] = usernames_to_id[delegate_username]
            resp = client.post(f"/api/employees/{employee_id}/availability/intervals", json=payload)
            if resp.status_code >= 400:
                print(f"предупреждение: интервал {kind} для {username} не создан: {resp.text}", file=sys.stderr)
            else:
                print(f"интервал создан: {username} — {kind}")

        print(f"готово: {len(usernames_to_id)} сотрудников, {len(site_group_id) + len(MANAGERS)} групп")


if __name__ == "__main__":
    main()
