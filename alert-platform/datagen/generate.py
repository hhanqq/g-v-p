"""Генератор синтетического потока алертов — раздел 18 спецификации.

Пример:
    python -m datagen.generate --days 1 --seed 42 --out out/day1.jsonl

Формат вывода — JSON Lines (раздел 18.6): одна строка — одно сообщение
с метаданными source, sent_at, raw_body, scenario_id, ground_truth.
Поле ground_truth существует только для оценки качества и симулятора
правил; при реальной отправке в шлюз платформы оно отбрасывается.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
from datetime import date, datetime, time, timedelta
from pathlib import Path

import yaml

from .inventory_builder import Inventory, build_inventory
from .render import render_body
from .scenarios import SCENARIOS

HERE = Path(__file__).parent

# Суточный профиль нагрузки (раздел 18.5): всплеск утром, провал ночью.
HOURLY_WEIGHTS = [
    0.3, 0.2, 0.2, 0.2, 0.3, 0.5, 1.0, 1.8, 2.2, 1.8, 1.5, 1.4,
    1.3, 1.3, 1.4, 1.5, 1.6, 1.5, 1.2, 1.0, 0.8, 0.6, 0.5, 0.4,
]

ZBX_SEVERITY_BY_LEVEL = {"noise": "Warning", "low": "Average", "significant": "High", "critical": "Disaster"}
SW_SEVERITY_BY_LEVEL = {"noise": "Warning", "low": "Warning", "significant": "Critical", "critical": "Serious"}

SCENARIO_LEVEL = {
    "p1_switch_cascade": "significant",
    "p0_rig_power_loss": "critical",
    "site_outage_ambiguous": "critical",
    "p2_disk_fill": "significant",
    "flapping_interface": "low",
    "package_update_spike": "significant",
    "maintenance_window": "low",
    "duplicate_cross_system": "significant",
    "object_outside_cmdb": "significant",
    "unrelated_single": "significant",
}


def load_yaml(path: Path) -> dict:
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def load_templates() -> dict:
    return {
        "zabbix": load_yaml(HERE / "templates" / "zabbix.yaml"),
        "solarwinds": load_yaml(HERE / "templates" / "solarwinds.yaml"),
    }


def load_scenario_defs() -> dict:
    defs = {}
    for path in sorted((HERE / "scenarios").glob("*.yaml")):
        cfg = load_yaml(path)
        defs[cfg["scenario"]] = cfg
    return defs


def sample_time_in_day(rnd: random.Random, day: date, spike_hours: set[int]) -> datetime:
    weights = [w * 3 if h in spike_hours else w for h, w in enumerate(HOURLY_WEIGHTS)]
    hour = rnd.choices(range(24), weights=weights, k=1)[0]
    return datetime.combine(day, time(hour, rnd.randint(0, 59), rnd.randint(0, 59)))


def resolve_object(inv: Inventory, object_id: str | None):
    if object_id is None:
        return None
    if object_id in inv.hosts:
        return inv.hosts[object_id]
    return inv.switches.get(object_id)


def dedup_key(site: str, object_id: str | None, component: str | None, symptom_class: str) -> str | None:
    if object_id is None:
        return None
    raw = f"{site}|{object_id}|{component or ''}|{symptom_class}"
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def format_duration(seconds: float) -> str:
    seconds = int(seconds)
    h, rem = divmod(seconds, 3600)
    m, s = divmod(rem, 60)
    if h:
        return f"{h}h {m}m {s}s"
    return f"{m}m {s}s"


def build_context(system: str, symptom_class: str, action: str, event: dict, inv: Inventory) -> dict:
    obj = resolve_object(inv, event["object_id"])
    node_name = event.get("node_name") or (obj.name if obj else "unknown")
    ip = obj.ip if obj else event["extra"].get("ghost_ip", "0.0.0.0")
    ctx = {"ts": event["ts"], "component": event.get("component") or ""}
    if system == "zabbix":
        ctx["host"] = node_name
        ctx["ip"] = ip
        if symptom_class == "host_unreachable":
            ctx["trigger_name"] = f"{node_name}: Unavailable by ICMP ping"
        elif symptom_class == "disk_space":
            ctx["threshold"] = event["extra"]["threshold"]
            ctx["value"] = event["extra"]["value"]
        if action == "resolved":
            ctx["duration"] = format_duration(event["extra"].get("duration_s", 60))
    else:
        ctx["node"] = node_name
        ctx["ip"] = ip
        if action == "resolved":
            ctx["duration"] = format_duration(event["extra"].get("duration_s", 60))
    return ctx


def render_event(rnd: random.Random, templates: dict, event: dict, inv: Inventory, site_instance: dict) -> dict:
    system, symptom_class, action = event["system"], event["symptom_class"], event["action"]
    tpl_root = templates[system]
    severity_level = SCENARIO_LEVEL.get(event["scenario_id"], event.get("level", "noise"))
    severity_map = ZBX_SEVERITY_BY_LEVEL if system == "zabbix" else SW_SEVERITY_BY_LEVEL
    severity = severity_map[severity_level]
    ctx = build_context(system, symptom_class, action, event, inv)
    ctx["severity"] = severity
    template_text = tpl_root["templates"][symptom_class][action]
    raw_body = render_body(template_text, ctx, rnd)

    site = event["site"]
    dkey = dedup_key(site, event["object_id"], event.get("component"), symptom_class)
    return {
        "sent_at": event["ts"].replace(microsecond=0).isoformat() + "Z",
        "source": {"system": system, "instance": site_instance[system][site]},
        "raw_body": raw_body,
        "scenario_id": event["scenario_id"],
        "ground_truth": {
            "site": site,
            "object_id": event["object_id"],
            "component": event.get("component"),
            "symptom_class": symptom_class,
            "action": action,
            "role": event["role"],
            "incident_group": event["incident_group"],
            "dedup_key": dkey,
            "severity_level": severity_level,
        },
    }


def gen_noise_events(rnd: random.Random, inv: Inventory, day: date, count: int, level: str,
                      spike_hours: set[int], self_resolving: bool) -> list[dict]:
    hosts = list(inv.hosts.values())
    switches = list(inv.switches.values())
    events = []
    for _ in range(count):
        ts = sample_time_in_day(rnd, day, spike_hours)
        if rnd.random() < 0.7:
            host = rnd.choice(hosts)
            options = ["host_unreachable"]
            if host.kind == "server":
                options += ["service_down", "disk_space"]
            else:
                options += ["power_lost"]
            symptom = rnd.choice(options)
            if symptom == "disk_space":
                disks = [c for c in host.components if c in ("C:", "D:")] or ["C:"]
                component = rnd.choice(disks)
            elif symptom in ("service_down", "power_lost"):
                component = rnd.choice(host.components)
            else:
                component = None
            extra = {"threshold": 70, "value": rnd.randint(70, 79)} if symptom == "disk_space" else {}
            base = dict(system="zabbix", symptom_class=symptom, action="firing", ts=ts,
                        object_id=host.id, site=host.site, component=component,
                        scenario_id=f"noise:{level}", incident_group=None, role=level,
                        node_name=host.name, extra=extra, level=level)
        else:
            sw = rnd.choice(switches)
            symptom = rnd.choice(["node_down", "interface_down"])
            component = f"Gi0/{rnd.randint(1, 24)}" if symptom == "interface_down" else None
            base = dict(system="solarwinds", symptom_class=symptom, action="firing", ts=ts,
                        object_id=sw.id, site=sw.site, component=component,
                        scenario_id=f"noise:{level}", incident_group=None, role=level,
                        node_name=sw.name, extra={}, level=level)
        events.append(base)
        if self_resolving:
            res_delay = rnd.uniform(15, 110)
            res = dict(base)
            res["action"] = "resolved"
            res["ts"] = ts + timedelta(seconds=res_delay)
            res["extra"] = {**base["extra"], "duration_s": res_delay}
            events.append(res)
    return events


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--days", type=int, default=1)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--rate-per-day", type=int, default=5400)
    parser.add_argument("--out", type=str, required=True)
    parser.add_argument("--start", type=str, default=None, help="YYYY-MM-DD, по умолчанию сегодня")
    args = parser.parse_args()

    rnd = random.Random(args.seed)
    inv_cfg = load_yaml(HERE / "inventory.yaml")
    inv = build_inventory(inv_cfg, args.seed)
    templates = load_templates()
    scenario_defs = load_scenario_defs()

    site_instance = {
        "zabbix": {code: f"zbx-{code}-01" for code in inv.sites},
        "solarwinds": {code: f"sw-mon-{code}-01" for code in inv.sites},
    }

    start_day = date.fromisoformat(args.start) if args.start else date.today()
    total_budget = round(args.rate_per_day * args.days)

    raw_events: list[dict] = []
    for d in range(args.days):
        day = start_day + timedelta(days=d)
        spike_hours = set(rnd.sample(range(24), k=2))

        raw_events += gen_noise_events(rnd, inv, day, round(total_budget * 0.65 / args.days / 2),
                                        "noise", spike_hours, self_resolving=True)
        raw_events += gen_noise_events(rnd, inv, day, round(total_budget * 0.20 / args.days),
                                        "low", spike_hours, self_resolving=False)
        raw_events += gen_noise_events(rnd, inv, day, round(total_budget * 0.12 / args.days),
                                        "significant", spike_hours, self_resolving=False)

        for scenario_id, gen_fn in SCENARIOS.items():
            cfg = scenario_defs[scenario_id]
            if rnd.random() < cfg["probability_per_day"]:
                anchor = sample_time_in_day(rnd, day, spike_hours)
                raw_events += gen_fn(rnd, inv, cfg["params"], anchor)

    raw_events.sort(key=lambda e: e["ts"])

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    counts: dict[str, int] = {}
    with out_path.open("w", encoding="utf-8") as f:
        for event in raw_events:
            record = render_event(rnd, templates, event, inv, site_instance)
            f.write(json.dumps(record, ensure_ascii=False) + "\n")
            key = event["scenario_id"]
            counts[key] = counts.get(key, 0) + 1

    print(f"Записано {len(raw_events)} сообщений в {out_path}")
    print(f"Объектов в CMDB: {len(inv.hosts)} хостов, {len(inv.switches)} коммутаторов, "
          f"{len(inv.services)} сервисов")
    for key, n in sorted(counts.items(), key=lambda kv: -kv[1]):
        print(f"  {key}: {n}")


if __name__ == "__main__":
    main()
