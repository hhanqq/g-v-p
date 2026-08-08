"""Раздел 18.4 — обязательный набор сценариев инцидентов.

Каждая функция читает свои параметры из datagen/scenarios/<id>.yaml и
возвращает список "сырых событий" (RawEvent) — без рендера текста, только
факты: система-источник, симптом, объект, момент времени, роль в инциденте.
Рендер в текст источника делается отдельно в generate.py, там же
рассчитывается dedup_key для ground_truth.
"""
from __future__ import annotations

import random
from datetime import datetime, timedelta

from .inventory_builder import Inventory


def _uniform_int(rnd: random.Random, bounds: list[int]) -> float:
    return rnd.uniform(bounds[0], bounds[1])


def _new_group(scenario_id: str, rnd: random.Random) -> str:
    # Детерминированный суррогат id группы — зависит только от seed, не от os.urandom.
    return f"{scenario_id}-{rnd.randrange(16 ** 8):08x}"


def ev(system, symptom_class, action, ts, object_id, site, component, scenario_id,
       group, role, node_name=None, extra=None):
    return {
        "system": system, "symptom_class": symptom_class, "action": action, "ts": ts,
        "object_id": object_id, "site": site, "component": component,
        "scenario_id": scenario_id, "incident_group": group, "role": role,
        "node_name": node_name, "extra": extra or {},
    }


def gen_p1_switch_cascade(rnd, inv: Inventory, params, anchor: datetime):
    site = rnd.choice(list(inv.sites))
    access_ids = inv.access_switches_of_site(site)
    if not access_ids:
        return []
    switch_id = rnd.choice(access_ids)
    group = _new_group("p1_switch_cascade", rnd)
    events = [ev("solarwinds", "node_down", "firing", anchor, switch_id, site, None,
                 "p1_switch_cascade", group, "root", node_name=inv.switches[switch_id].name)]

    behind = inv.objects_behind_switch(switch_id)
    rnd.shuffle(behind)
    n_symptoms = min(int(_uniform_int(rnd, params["symptom_count"])), len(behind))
    affected = behind[:n_symptoms]
    for host_id in affected:
        delay = _uniform_int(rnd, params["symptom_delay"])
        events.append(ev("zabbix", "host_unreachable", "firing", anchor + timedelta(seconds=delay),
                          host_id, site, None, "p1_switch_cascade", group, "symptom",
                          node_name=inv.hosts[host_id].name))

    touched_services = set()
    for host_id in affected:
        touched_services.update(inv.services_on(host_id))
    n_services = int(_uniform_int(rnd, params["service_count"]))
    for svc_id in sorted(touched_services)[:n_services]:
        delay = _uniform_int(rnd, params["service_delay"])
        host_id = next((h for h in inv.services[svc_id].object_ids if h in affected), affected[0])
        events.append(ev("zabbix", "service_down", "firing", anchor + timedelta(seconds=delay),
                          host_id, site, svc_id, "p1_switch_cascade", group, "symptom",
                          node_name=inv.hosts[host_id].name))

    res_delay = _uniform_int(rnd, params["resolution_delay_min"]) * 60
    events.append(ev("solarwinds", "node_down", "resolved", anchor + timedelta(seconds=res_delay),
                      switch_id, site, None, "p1_switch_cascade", group, "resolution",
                      node_name=inv.switches[switch_id].name, extra={"duration_s": res_delay}))
    return events


def gen_p0_rig_power_loss(rnd, inv: Inventory, params, anchor: datetime):
    site = rnd.choice(list(inv.sites))
    rig_ids = inv.rigs_of_site(site)
    if not rig_ids:
        return []
    rig_id = rnd.choice(rig_ids)
    controllers = inv.controllers_of_rig(rig_id)
    if not controllers:
        return []
    group = _new_group("p0_rig_power_loss", rnd)
    events = []
    for i, ctrl_id in enumerate(controllers):
        delay = _uniform_int(rnd, params["controller_delay"])
        role = "root" if i == 0 else "symptom"
        events.append(ev("zabbix", "power_lost", "firing", anchor + timedelta(seconds=delay),
                          ctrl_id, site, "PLC", "p0_rig_power_loss", group, role,
                          node_name=inv.hosts[ctrl_id].name))
    res_delay = _uniform_int(rnd, params["resolution_delay_min"]) * 60
    for ctrl_id in controllers:
        events.append(ev("zabbix", "power_lost", "resolved", anchor + timedelta(seconds=res_delay),
                          ctrl_id, site, "PLC", "p0_rig_power_loss", group, "resolution",
                          node_name=inv.hosts[ctrl_id].name, extra={"duration_s": res_delay}))
    return events


def gen_site_outage_ambiguous(rnd, inv: Inventory, params, anchor: datetime):
    site = rnd.choice(list(inv.sites))
    core_id = inv.core_switch_of_site(site)
    hosts_of_site = inv.hosts_of_site(site)
    if not hosts_of_site:
        return []
    power_host = rnd.choice(hosts_of_site)
    group = _new_group("site_outage_ambiguous", rnd)
    j = _uniform_int(rnd, params["jitter_seconds"])
    events = [
        ev("solarwinds", "node_down", "firing", anchor, core_id, site, None,
           "site_outage_ambiguous", group, "candidate_cause", node_name=inv.switches[core_id].name),
        ev("zabbix", "power_lost", "firing", anchor + timedelta(seconds=j), power_host, site, "PDU",
           "site_outage_ambiguous", group, "candidate_cause", node_name=inv.hosts[power_host].name),
    ]
    res_delay = _uniform_int(rnd, params["resolution_delay_min"]) * 60
    events.append(ev("solarwinds", "node_down", "resolved", anchor + timedelta(seconds=res_delay),
                      core_id, site, None, "site_outage_ambiguous", group, "resolution",
                      node_name=inv.switches[core_id].name, extra={"duration_s": res_delay}))
    return events


def gen_p2_disk_fill(rnd, inv: Inventory, params, anchor: datetime):
    servers = [h for h in inv.hosts.values() if h.kind == "server"]
    host = rnd.choice(servers)
    component = rnd.choice([c for c in host.components if c in ("C:", "D:")] or ["C:"])
    group = _new_group("p2_disk_fill", rnd)
    events = []
    t = anchor
    thresholds = params["thresholds"]
    for i, threshold in enumerate(thresholds):
        value = threshold + rnd.randint(0, 3)
        events.append(ev("zabbix", "disk_space", "firing", t, host.id, host.site, component,
                          "p2_disk_fill", group, "root" if i == 0 else "repeat",
                          node_name=host.name, extra={"threshold": threshold, "value": value}))
        t = t + timedelta(minutes=_uniform_int(rnd, params["step_delay_min"]))
    return events


def gen_flapping_interface(rnd, inv: Inventory, params, anchor: datetime):
    hosts = [h for h in inv.hosts.values() if "eth0" in h.components or "eth1" in h.components]
    switches = list(inv.switches.values())
    use_host = hosts and rnd.random() < 0.5
    if use_host:
        target = rnd.choice(hosts)
        node_name, site, obj_id = target.name, target.site, target.id
        component = "eth0"
    else:
        target = rnd.choice(switches)
        node_name, site, obj_id = target.name, target.site, target.id
        component = f"Gi0/{rnd.randint(1, 24)}"
    group = _new_group("flapping_interface", rnd)
    n_toggles = int(_uniform_int(rnd, params["toggle_count"]))
    window_s = params["window_minutes"] * 60
    events = []
    for i in range(n_toggles):
        t = anchor + timedelta(seconds=(window_s / n_toggles) * i)
        action = "firing" if i % 2 == 0 else "resolved"
        role = "root" if i == 0 else ("flap_counted" if i < 2 else "flap_suppressed")
        events.append(ev("solarwinds", "interface_down", action, t, obj_id, site, component,
                          "flapping_interface", group, role, node_name=node_name))
    return events


def gen_package_update_spike(rnd, inv: Inventory, params, anchor: datetime):
    candidates = [h for h in inv.hosts.values()
                  if any(p["updated_yesterday"] for p in h.packages.values())]
    if len(candidates) < params["min_affected"]:
        return []
    share = _uniform_int(rnd, params["affected_share"])
    n = max(params["min_affected"], int(len(candidates) * share))
    affected = rnd.sample(candidates, k=min(n, len(candidates)))
    group = _new_group("package_update_spike", rnd)
    events = []
    for i, host in enumerate(affected):
        delay = rnd.uniform(0, params["spike_window_min"] * 60)
        symptom = "service_down" if host.kind == "server" else "host_unreachable"
        component = rnd.choice(host.components) if host.kind == "server" else None
        events.append(ev("zabbix", symptom, "firing", anchor + timedelta(seconds=delay),
                          host.id, host.site, component, "package_update_spike", group,
                          "root" if i == 0 else "symptom", node_name=host.name))
    return events


def gen_maintenance_window(rnd, inv: Inventory, params, anchor: datetime):
    host = rnd.choice(list(inv.hosts.values()))
    group = _new_group("maintenance_window", rnd)
    duration_min = _uniform_int(rnd, params["window_duration_min"])
    n_alerts = int(_uniform_int(rnd, params["alert_count"]))
    events = []
    for i in range(n_alerts):
        t = anchor + timedelta(minutes=rnd.uniform(0, duration_min))
        events.append(ev("zabbix", "host_unreachable", "firing", t, host.id, host.site, None,
                          "maintenance_window", group, "expected_maintenance", node_name=host.name,
                          extra={"window_start": anchor.isoformat(), "window_minutes": duration_min}))
    return events


def gen_duplicate_cross_system(rnd, inv: Inventory, params, anchor: datetime):
    site = rnd.choice(list(inv.sites))
    access_ids = inv.access_switches_of_site(site)
    if not access_ids:
        return []
    switch_id = rnd.choice(access_ids)
    group = _new_group("duplicate_cross_system", rnd)
    j = _uniform_int(rnd, params["jitter_seconds"])
    return [
        ev("solarwinds", "node_down", "firing", anchor, switch_id, site, None,
           "duplicate_cross_system", group, "duplicate", node_name=inv.switches[switch_id].name),
        ev("zabbix", "host_unreachable", "firing", anchor + timedelta(seconds=j), switch_id, site, None,
           "duplicate_cross_system", group, "duplicate", node_name=inv.switches[switch_id].name),
    ]


def gen_object_outside_cmdb(rnd, inv: Inventory, params, anchor: datetime):
    site = rnd.choice(list(inv.sites))
    ghost_ip = f"10.{rnd.randint(200, 250)}.{rnd.randint(1, 254)}.{rnd.randint(2, 254)}"
    ghost_name = f"unknown-{rnd.randint(1000, 9999)}"
    group = _new_group("object_outside_cmdb", rnd)
    return [ev("zabbix", "host_unreachable", "firing", anchor, None, site, None,
                "object_outside_cmdb", group, "quarantine", node_name=ghost_name,
                extra={"ghost_ip": ghost_ip})]


def gen_unrelated_single(rnd, inv: Inventory, params, anchor: datetime):
    host = rnd.choice(list(inv.hosts.values()))
    symptom = "host_unreachable" if host.kind == "controller" else rnd.choice(["host_unreachable", "service_down"])
    component = rnd.choice(host.components) if symptom == "service_down" else None
    group = _new_group("unrelated_single", rnd)
    return [ev("zabbix", symptom, "firing", anchor, host.id, host.site, component,
                "unrelated_single", group, "single", node_name=host.name)]


SCENARIOS = {
    "p1_switch_cascade": gen_p1_switch_cascade,
    "p0_rig_power_loss": gen_p0_rig_power_loss,
    "site_outage_ambiguous": gen_site_outage_ambiguous,
    "p2_disk_fill": gen_p2_disk_fill,
    "flapping_interface": gen_flapping_interface,
    "package_update_spike": gen_package_update_spike,
    "maintenance_window": gen_maintenance_window,
    "duplicate_cross_system": gen_duplicate_cross_system,
    "object_outside_cmdb": gen_object_outside_cmdb,
    "unrelated_single": gen_unrelated_single,
}
