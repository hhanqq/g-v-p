"""Разворачивает datagen/inventory.yaml в конкретный граф объектов.

Детерминирован по seed: одинаковый seed => побитово одинаковый граф.
Топология связна намеренно (раздел 18.2): сервер/контроллер -> access-switch
-> core-switch -> площадка. Это даёт материал для топологической и
подсетевой осей корреляции (раздел 6.2).
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field


@dataclass
class Switch:
    id: str
    name: str
    role: str  # core | access
    site: str
    subnet: str
    ip: str
    parent_id: str | None  # access -> id core-switch; core -> None


@dataclass
class Host:
    id: str
    name: str
    kind: str  # server | controller
    site: str
    subnet: str
    ip: str
    access_switch_id: str
    fqdn: str | None
    inventory_id: str | None
    os: str | None
    components: list[str]
    owners: list[str]
    rig_id: str | None = None
    packages: dict[str, dict] = field(default_factory=dict)


@dataclass
class Rig:
    id: str
    name: str
    site: str


@dataclass
class Service:
    id: str
    name: str
    criticality: str
    owners: list[str]
    object_ids: list[str]


@dataclass
class Site:
    code: str
    name: str
    subsidiary: str
    timezone: str


@dataclass
class Inventory:
    seed: int
    sites: dict[str, Site]
    subsidiaries: dict[str, str]
    switches: dict[str, Switch]
    hosts: dict[str, Host]
    rigs: dict[str, Rig]
    services: dict[str, Service]

    def objects_behind_switch(self, switch_id: str) -> list[str]:
        """Хосты за коммутатором: если это access — прямые дети;
        если core — все хосты за всеми access этой площадки."""
        sw = self.switches[switch_id]
        if sw.role == "access":
            return [h.id for h in self.hosts.values() if h.access_switch_id == switch_id]
        result = []
        for asw in self.switches.values():
            if asw.role == "access" and asw.parent_id == switch_id:
                result.extend(self.objects_behind_switch(asw.id))
        return result

    def services_on(self, object_id: str) -> list[str]:
        return [s.id for s in self.services.values() if object_id in s.object_ids]

    def hosts_of_site(self, site_code: str) -> list[str]:
        return [h.id for h in self.hosts.values() if h.site == site_code]

    def access_switches_of_site(self, site_code: str) -> list[str]:
        return [s.id for s in self.switches.values() if s.site == site_code and s.role == "access"]

    def core_switch_of_site(self, site_code: str) -> str:
        return next(s.id for s in self.switches.values() if s.site == site_code and s.role == "core")

    def controllers_of_rig(self, rig_id: str) -> list[str]:
        return [h.id for h in self.hosts.values() if h.rig_id == rig_id]

    def rigs_of_site(self, site_code: str) -> list[str]:
        return [r.id for r in self.rigs.values() if r.site == site_code]


def build_inventory(cfg: dict, seed: int) -> Inventory:
    rnd = random.Random(seed)

    subsidiaries = {s["code"]: s["name"] for s in cfg["subsidiaries"]}
    sites = {
        s["code"]: Site(code=s["code"], name=s["name"], subsidiary=s["subsidiary"], timezone=s["timezone"])
        for s in cfg["sites"]
    }
    dual_share = cfg["ownership"]["dual_owner_share"]
    orphan_share = cfg["ownership"]["orphan_owner_share"]

    def pick_owners(primary: str) -> list[str]:
        r = rnd.random()
        if r < orphan_share:
            return []
        if r < orphan_share + dual_share:
            others = [c for c in subsidiaries if c != primary]
            return sorted({primary, rnd.choice(others)})
        return [primary]

    switches: dict[str, Switch] = {}
    hosts: dict[str, Host] = {}
    rigs: dict[str, Rig] = {}

    ip_counters: dict[str, int] = {}

    def next_ip(site_octet: int, subnet_index: int) -> str:
        key = f"{site_octet}.{subnet_index}"
        ip_counters[key] = ip_counters.get(key, 1) + 1
        return f"10.{site_octet}.{subnet_index}.{ip_counters[key]}"

    id_gap = cfg["identifier_gaps"]["missing_fqdn_or_inventory_share"]
    comp_pool = cfg["servers"]["component_pool"]
    comp_range = cfg["servers"]["components_per_object"]
    pkg_pool = cfg["software"]["package_pool"]
    pkg_yesterday_share = cfg["software"]["updated_yesterday_share"]

    def gen_components() -> list[str]:
        n = rnd.randint(comp_range["min"], comp_range["max"])
        return [rnd.choice(comp_pool) for _ in range(n)]

    def gen_packages() -> dict[str, dict]:
        pkgs = {}
        for pkg in rnd.sample(pkg_pool, k=min(2, len(pkg_pool))):
            updated_yesterday = rnd.random() < pkg_yesterday_share
            pkgs[pkg] = {
                "version": f"{rnd.randint(1, 9)}.{rnd.randint(0, 20)}.{rnd.randint(0, 9)}",
                "updated_yesterday": updated_yesterday,
            }
        return pkgs

    def maybe_drop_ids(fqdn: str, inv: str) -> tuple[str | None, str | None]:
        if rnd.random() < id_gap:
            return (None, inv) if rnd.random() < 0.5 else (fqdn, None)
        return fqdn, inv

    for site in cfg["sites"]:
        site_code = site["code"]
        site_octet = site["site_octet"]
        n_subnets = rnd.randint(cfg["subnets"]["per_site"]["min"], cfg["subnets"]["per_site"]["max"])
        subnet_indices = rnd.sample(range(1, 60), k=n_subnets)

        core_id = f"sw-{site_code}-core-01"
        core_subnet_idx = subnet_indices[0]
        switches[core_id] = Switch(
            id=core_id, name=core_id, role="core", site=site_code,
            subnet=f"10.{site_octet}.{core_subnet_idx}.0/24",
            ip=next_ip(site_octet, core_subnet_idx), parent_id=None,
        )

        n_access = rnd.randint(cfg["network"]["access_switches_per_site"]["min"],
                                cfg["network"]["access_switches_per_site"]["max"])
        access_ids = []
        access_subnet_idx: dict[str, int] = {}
        for i in range(1, n_access + 1):
            sub_idx = subnet_indices[i % n_subnets]
            aid = f"sw-{site_code}-acc-{i:02d}"
            switches[aid] = Switch(
                id=aid, name=aid, role="access", site=site_code,
                subnet=f"10.{site_octet}.{sub_idx}.0/24",
                ip=next_ip(site_octet, sub_idx), parent_id=core_id,
            )
            access_ids.append(aid)
            access_subnet_idx[aid] = sub_idx

        n_servers = rnd.randint(cfg["servers"]["per_site"]["min"], cfg["servers"]["per_site"]["max"])
        name_pool = cfg["servers"]["name_pool"]
        name_seq: dict[str, int] = {}
        for _ in range(n_servers):
            base = rnd.choice(name_pool)
            name_seq[base] = name_seq.get(base, 0) + 1
            name = f"{base}-{name_seq[base]:02d}"
            asw = rnd.choice(access_ids)
            sub_idx = access_subnet_idx[asw]  # хост наследует подсеть своего access-switch
            ip = next_ip(site_octet, sub_idx)
            fqdn, inv = maybe_drop_ids(f"{name}.{site_code}.local", f"INV-{rnd.randint(100000, 999999)}")
            hid = f"{site_code}/{name}"
            hosts[hid] = Host(
                id=hid, name=name, kind="server", site=site_code,
                subnet=f"10.{site_octet}.{sub_idx}.0/24", ip=ip,
                access_switch_id=asw, fqdn=fqdn, inventory_id=inv,
                os=rnd.choice(cfg["servers"]["os_pool"]),
                components=gen_components(), owners=pick_owners(site["subsidiary"]),
                packages=gen_packages(),
            )

        n_rigs = rnd.randint(cfg["drilling"]["rigs_per_site"]["min"], cfg["drilling"]["rigs_per_site"]["max"])
        for rseq in range(1, n_rigs + 1):
            rig_id = f"{site_code}/rig{rseq}"
            rigs[rig_id] = Rig(id=rig_id, name=f"Буровая №{rseq}", site=site_code)
            n_ctrl = rnd.randint(cfg["drilling"]["controllers_per_rig"]["min"],
                                  cfg["drilling"]["controllers_per_rig"]["max"])
            for cseq in range(1, n_ctrl + 1):
                name = f"well{rseq}-plc-{cseq:02d}"
                asw = rnd.choice(access_ids)
                sub_idx = access_subnet_idx[asw]  # контроллер наследует подсеть своего access-switch
                ip = next_ip(site_octet, sub_idx)
                fqdn, inv = maybe_drop_ids(f"{name}.{site_code}.local", f"INV-{rnd.randint(100000, 999999)}")
                hid = f"{site_code}/{name}"
                hosts[hid] = Host(
                    id=hid, name=name, kind="controller", site=site_code,
                    subnet=f"10.{site_octet}.{sub_idx}.0/24", ip=ip,
                    access_switch_id=asw, fqdn=fqdn, inventory_id=inv, os=None,
                    components=[rnd.choice(cfg["drilling"]["components"])],
                    owners=pick_owners(site["subsidiary"]), rig_id=rig_id,
                    packages=gen_packages(),
                )

    services: dict[str, Service] = {}
    all_host_ids = list(hosts.keys())
    for i in range(1, cfg["services"]["count"] + 1):
        sid = f"svc-{i:02d}"
        n_obj = rnd.randint(cfg["services"]["objects_per_service"]["min"],
                             cfg["services"]["objects_per_service"]["max"])
        objs = rnd.sample(all_host_ids, k=min(n_obj, len(all_host_ids)))
        primary_site = hosts[objs[0]].site
        services[sid] = Service(
            id=sid, name=f"{rnd.choice(cfg['services']['name_pool'])} — {sites[primary_site].name}",
            criticality=rnd.choice(cfg["services"]["criticality_levels"]),
            owners=pick_owners(sites[primary_site].subsidiary),
            object_ids=objs,
        )

    return Inventory(seed=seed, sites=sites, subsidiaries=subsidiaries, switches=switches,
                      hosts=hosts, rigs=rigs, services=services)
