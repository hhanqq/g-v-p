"""Тесты нового JSON API платформы (Этап 1) — services/api/app_api.py.
FastAPI TestClient поверх временной SQLite-БД (файл, не :memory: —
несколько Session() из get_session() должны видеть одни и те же данные).
LDAP не поднимаем — packages.common.ldap_auth.authenticate замокан:
эти тесты проверяют наш код (роуты, сессии, ORM-запросы), а не сам LDAP
(тот уже проверен вживую отдельно, раздел «Безопасность»)."""
from __future__ import annotations

import os
import tempfile
from datetime import datetime
from unittest.mock import patch

import pytest

_tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
os.environ.setdefault("DATABASE_URL", f"sqlite:///{_tmp_db.name}")
os.environ.setdefault("CMDB_SEED", "42")

from fastapi.testclient import TestClient  # noqa: E402

import services.api.main as main_module  # noqa: E402
from packages.common.db import get_session  # noqa: E402
from packages.models.db import CmdbObject, Problem, Subscriber, Subscription  # noqa: E402

client = TestClient(main_module.app)


@pytest.fixture(autouse=True)
def _mock_ldap():
    with patch("packages.common.ldap_auth.authenticate", return_value=(True, True)):
        yield


def _login():
    resp = client.post("/api/auth/login", json={"username": "admin1", "password": "x"})
    assert resp.status_code == 200
    return resp


def test_login_sets_session_and_me_reflects_it():
    _login()
    me = client.get("/api/auth/me")
    assert me.status_code == 200
    assert me.json() == {"username": "admin1", "is_admin": True}


def test_bad_credentials_rejected():
    with patch("packages.common.ldap_auth.authenticate", return_value=(False, False)):
        resp = client.post("/api/auth/login", json={"username": "x", "password": "wrong"})
    assert resp.status_code == 401


def test_unauthenticated_request_is_401():
    anon = TestClient(main_module.app)
    resp = anon.get("/api/home/summary")
    assert resp.status_code == 401


def test_logout_clears_session():
    _login()
    client.post("/api/auth/logout")
    resp = client.get("/api/auth/me")
    assert resp.status_code == 401
    _login()  # восстанавливаем сессию для остальных тестов в этом файле


def test_home_summary_returns_dashboard_snapshot():
    _login()
    resp = client.get("/api/home/summary")
    assert resp.status_code == 200
    assert "events" in resp.json()
    assert "ai_scenarios" in resp.json()


def test_equipment_list_and_detail():
    _login()
    with get_session() as session:
        session.add(CmdbObject(id="well-01", kind="controller", site="brd-x", name="well-01",
                                equipment_type="скважина"))
        session.commit()
    listed = client.get("/api/equipment")
    assert listed.status_code == 200
    assert "well-01" in [e["id"] for e in listed.json()]

    detail = client.get("/api/equipment/well-01")
    assert detail.status_code == 200
    assert detail.json()["equipment_type"] == "скважина"
    assert detail.json()["related_problems"] == []


def test_equipment_detail_404_for_unknown_object():
    _login()
    resp = client.get("/api/equipment/does-not-exist")
    assert resp.status_code == 404


def test_employees_list_shows_availability_and_can_set_it():
    _login()
    with get_session() as session:
        sub = Subscriber(trueconf_username="empl1", access_token="tok-empl1", created_at=datetime.utcnow())
        session.add(sub)
        session.commit()
        employee_id = sub.id

    listed = client.get("/api/employees")
    assert listed.status_code == 200
    assert any(e["trueconf_username"] == "empl1" for e in listed.json())

    set_resp = client.post(f"/api/employees/{employee_id}/availability", json={"status": "vacation"})
    assert set_resp.status_code == 200

    detail = client.get(f"/api/employees/{employee_id}")
    assert detail.status_code == 200
    assert detail.json()["availability_history"][0]["status"] == "vacation"
    assert detail.json()["availability_history"][0]["source"] == "manual"


def test_analytics_summary_returns_bundled_stats():
    _login()
    resp = client.get("/api/analytics/summary")
    assert resp.status_code == 200
    body = resp.json()
    assert "alerts_over_time" in body
    assert "top_problem_objects" in body
    assert "sla_breach" in body


def test_incidents_and_alerts_endpoints_smoke():
    _login()
    assert client.get("/api/incidents").status_code == 200
    assert client.get("/api/alerts").status_code == 200
    assert "items" in client.get("/api/alerts").json()


def test_stub_endpoints_smoke():
    _login()
    assert client.get("/api/scenarios").status_code == 200
    assert client.get("/api/sla-rules").status_code == 200
    integrations = client.get("/api/integrations/status")
    assert integrations.status_code == 200
    names = [i["name"] for i in integrations.json()]
    assert "TrueConf" in names
    hr_row = next(i for i in integrations.json() if "HR" in i["name"])
    assert hr_row["status"] == "open_question"  # честно, не выдуманная интеграция


_VALID_GRAPH = ('{"nodes": ['
                '{"id": "1", "type": "condition", "data": {"priority_min": "P1"}}, '
                '{"id": "2", "type": "notify", "data": {"employee_id": 1}}], '
                '"edges": [{"source": "1", "target": "2"}]}')
_BRANCHING_GRAPH = ('{"nodes": ['
                     '{"id": "1", "type": "condition", "data": {}}, '
                     '{"id": "2", "type": "notify", "data": {}}, '
                     '{"id": "3", "type": "notify", "data": {}}], '
                     '"edges": [{"source": "1", "target": "2"}, {"source": "1", "target": "3"}]}')


def test_scenario_crud_and_activate_flow():
    _login()
    created = client.post("/api/scenarios", json={"name": "Ночная эскалация", "graph_json": _VALID_GRAPH})
    assert created.status_code == 200
    scenario_id = created.json()["id"]

    detail = client.get(f"/api/scenarios/{scenario_id}")
    assert detail.status_code == 200
    assert detail.json()["status"] == "draft"
    assert detail.json()["name"] == "Ночная эскалация"

    activated = client.post(f"/api/scenarios/{scenario_id}/activate")
    assert activated.status_code == 200
    assert client.get(f"/api/scenarios/{scenario_id}").json()["status"] == "active"

    deactivated = client.post(f"/api/scenarios/{scenario_id}/deactivate")
    assert deactivated.status_code == 200
    assert client.get(f"/api/scenarios/{scenario_id}").json()["status"] == "draft"


def test_scenario_activate_rejects_graph_that_is_not_a_chain():
    _login()
    created = client.post("/api/scenarios", json={"name": "Ветвление", "graph_json": _BRANCHING_GRAPH})
    scenario_id = created.json()["id"]
    resp = client.post(f"/api/scenarios/{scenario_id}/activate")
    assert resp.status_code == 400
    assert client.get(f"/api/scenarios/{scenario_id}").json()["status"] == "draft"


def test_scenario_update_deactivates_active_scenario_on_graph_change():
    _login()
    created = client.post("/api/scenarios", json={"name": "Правка на лету", "graph_json": _VALID_GRAPH})
    scenario_id = created.json()["id"]
    client.post(f"/api/scenarios/{scenario_id}/activate")
    assert client.get(f"/api/scenarios/{scenario_id}").json()["status"] == "active"

    updated = client.put(f"/api/scenarios/{scenario_id}", json={"graph_json": _VALID_GRAPH})
    assert updated.status_code == 200
    # раздел И5 — изменённый граф не проверен заново, активный статус снимается
    assert client.get(f"/api/scenarios/{scenario_id}").json()["status"] == "draft"


def test_scenario_get_404_for_unknown_id():
    _login()
    assert client.get("/api/scenarios/999999").status_code == 404


def test_my_current_alerts_page_shows_only_routed_problems():
    with get_session() as session:
        sub = Subscriber(trueconf_username="alerts_viewer", access_token="tok-alerts-viewer",
                          created_at=datetime.utcnow())
        session.add(sub)
        session.flush()
        session.add(Subscription(subscriber_id=sub.id, created_at=datetime.utcnow()))  # catch-all
        session.add(Problem(dedup_key="my-alert-1", status="OPEN", symptom_class="node_down",
                             opened_at=datetime.utcnow(), last_seen_at=datetime.utcnow(),
                             repeat_count=1, toggle_count=0, priority="P1"))
        session.commit()

    forbidden = client.get("/alerts/alerts_viewer/", params={"token": "wrong"})
    assert forbidden.status_code == 403

    ok = client.get("/alerts/alerts_viewer/", params={"token": "tok-alerts-viewer"})
    assert ok.status_code == 200
    assert "node_down" in ok.text


def test_sla_rule_create_and_list():
    _login()
    created = client.post("/api/sla-rules", json={
        "name": "P0 критично", "priority": "P0", "response_minutes": 15, "resolution_minutes": 120,
    })
    assert created.status_code == 200
    listed = client.get("/api/sla-rules").json()
    assert any(r["name"] == "P0 критично" and r["response_minutes"] == 15 for r in listed)
