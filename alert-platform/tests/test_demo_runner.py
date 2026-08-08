from fastapi.testclient import TestClient

from services.demo.main import app


client = TestClient(app)


def test_demo_catalog_contains_required_scenarios():
    response = client.get("/api/demo/scenarios")
    assert response.status_code == 200
    scenarios = response.json()
    assert "p1_switch_cascade" in scenarios
    assert "object_outside_cmdb" in scenarios


def test_unknown_demo_scenario_is_rejected_without_scheduling():
    response = client.post("/api/trigger/not-a-scenario")
    assert response.status_code == 404
