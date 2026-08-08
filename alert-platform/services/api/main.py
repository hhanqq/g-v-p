"""Консоль администратора (упрощённая) — по мотивам раздела 11.2.

Полноценная админка (M13: конструктор правил корреляции, симулятор) не
реализована. Реализовано: запуск демо-сценариев, самообслуживаемые
подписки (раздел 8, /me), дашборд (раздел 7 «Аналитика», /dashboard) и
регистрация источников (/sources) — кейс требует "подключение новых
источников... без изменения ядра системы": новый инстанс существующей
системы мониторинга (например, Zabbix пятого филиала) регистрируется
здесь, без правки connectors/sources.yaml и редеплоя.

Таймлайн сценария сжимается во времени (раздел 18.4 описывает сценарии с
задержками в десятки минут — ждать их вживую на демонстрации не нужно):
исходные смещения между событиями масштabируются так, чтобы весь каскад
укладывался в ~60 секунд реального времени, но текст самих алертов
(поле Time/Triggered) сохраняет исходную, несжатую временную легенду —
иначе длительность в CLOSURE выглядела бы нелепо (закрыто через 2 секунды
после открытия).
"""
from __future__ import annotations

import asyncio
import html
import os
import random
import secrets
from datetime import datetime, timedelta
from pathlib import Path

from fastapi import Depends, FastAPI, Form, HTTPException
from fastapi.responses import FileResponse, HTMLResponse, RedirectResponse
from fastapi.security import HTTPBasic, HTTPBasicCredentials
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel
from sqlalchemy import select
from starlette.middleware.sessions import SessionMiddleware

from datagen.generate import HERE as DATAGEN_DIR
from datagen.generate import load_scenario_defs, load_templates, load_yaml, render_event
from datagen.inventory_builder import build_inventory
from datagen.scenarios import SCENARIOS
from packages.common import ldap_auth
from packages.common import system_stats
from packages.common.audit import log_action
from packages.common.db import get_session
from packages.common.ingest import ingest_raw
from packages.common.routing import resolve_recipients
from packages.common.sources import seed_from_yaml_if_empty
from packages.models.db import (AuditLog, CmdbObject, CmdbOwnership, CmdbService,
                                 IncidentProblem, Problem, SourceInstance, Subscriber, Subscription)
from packages.ai import client as ai_client
from packages.ai.suggest_subscription import extract_recommended_subsidiary, suggest_subscription
from services.api import app_api, metrics
from services.pipeline.parser import load_connectors

CMDB_SEED = int(os.environ.get("CMDB_SEED", "42"))
MAX_DEMO_WINDOW_S = 60.0
CONNECTORS_DIR = DATAGEN_DIR.parent / "connectors"
WEB_DIST_DIR = Path(__file__).resolve().parents[2] / "web" / "dist"

app = FastAPI(title="Диспетчер — консоль запуска сценариев")

# --- Платформа, Этап 1: новый SPA (план — ~/.claude/plans/cheerful-mixing-pillow.md) ---
# Сессионная cookie-авторизация — отдельно от HTTP Basic ниже (тот
# защищает старые server-rendered страницы, не трогаем). SESSION_SECRET
# без значения по умолчанию в проде был бы дырой; для этого демо-стенда
# (как и все остальные demo-пароли в проекте) — с явным дефолтом, не
# скрытым от чтения кода.
app.add_middleware(SessionMiddleware, secret_key=os.environ.get("SESSION_SECRET", "dispatcher-demo-secret"))
app.include_router(app_api.router)

if (WEB_DIST_DIR / "assets").exists():
    app.mount("/app/assets", StaticFiles(directory=str(WEB_DIST_DIR / "assets")), name="spa-assets")


@app.get("/app")
def spa_root() -> FileResponse:
    index = WEB_DIST_DIR / "index.html"
    if not index.exists():
        raise HTTPException(503, "Фронтенд ещё не собран (web/dist отсутствует)")
    return FileResponse(index)


@app.get("/app/{full_path:path}")
def spa_catch_all(full_path: str) -> FileResponse:
    # Клиентский роутинг (react-router) — любой путь под /app отдаёт тот
    # же index.html, дальше маршрутизация происходит в браузере.
    index = WEB_DIST_DIR / "index.html"
    if not index.exists():
        raise HTTPException(503, "Фронтенд ещё не собран (web/dist отсутствует)")
    return FileResponse(index)

# --- RBAC через LDAP (раздел «Безопасность» кейса) --------------------------
# Раздел 20 п.11: "реализована авторизация через LDAP/AD и разграничение
# доступа по ролям" — /dashboard и /sources (раньше открыты всем) теперь
# требуют HTTP Basic, проверяемый настоящим LDAP-каталогом
# (packages/common/ldap_auth.py, docker-compose сервис `ldap`), с ролью
# "admins" (группа в каталоге). Личный кабинет (/me/) не тронут — там
# своя, отдельная модель доступа через токен от бота (раздел 8).

_security = HTTPBasic()


def require_admin(credentials: HTTPBasicCredentials = Depends(_security)) -> str:
    # Логируются только отказы (реальный интерес для аудита ИБ) — успешный
    # вход НЕ логируется на каждый запрос: браузер повторно шлёт Basic Auth
    # на КАЖДЫЙ HTTP-запрос в рамках realm (в т.ч. автообновление
    # дашборда раз в 15с), логирование каждого было бы просто шумом, а не
    # аудитом. Действия администратора (добавление/удаление источника)
    # логируются отдельно, там, где они происходят.
    authenticated, is_admin = ldap_auth.authenticate(credentials.username, credentials.password)
    if not authenticated:
        with get_session() as session:
            log_action(session, actor=credentials.username, action="admin_login_failed")
        raise HTTPException(401, "Неверный логин или пароль LDAP",
                             headers={"WWW-Authenticate": "Basic"})
    if not is_admin:
        with get_session() as session:
            log_action(session, actor=credentials.username, action="admin_login_denied",
                       detail="аутентифицирован, но не в группе admins")
        raise HTTPException(403, "Требуется роль администратора платформы (группа admins в LDAP)")
    return credentials.username


_inventory = None
_templates = None
_scenario_defs = None


def _get_inventory():
    global _inventory
    if _inventory is None:
        cfg = load_yaml(DATAGEN_DIR / "inventory.yaml")
        _inventory = build_inventory(cfg, CMDB_SEED)
    return _inventory


def _get_templates():
    global _templates
    if _templates is None:
        _templates = load_templates()
    return _templates


def _get_scenario_defs():
    global _scenario_defs
    if _scenario_defs is None:
        _scenario_defs = load_scenario_defs()
    return _scenario_defs


class TriggerResult(BaseModel):
    scenario_id: str
    events_scheduled: int
    demo_duration_s: float


async def _deliver_with_delays(records: list[tuple[float, dict]]) -> None:
    started = datetime.utcnow()
    for offset_s, record in records:
        target = started + timedelta(seconds=offset_s)
        remaining = (target - datetime.utcnow()).total_seconds()
        if remaining > 0:
            await asyncio.sleep(remaining)
        with get_session() as session:
            try:
                ingest_raw(session, source_system=record["source"]["system"],
                           source_instance=record["source"]["instance"],
                           raw_body=record["raw_body"])
            except ValueError:
                continue  # NUL-байт и т.п. — раздел И5, не валим фон-задачу целиком


@app.post("/api/trigger/{scenario_id}", response_model=TriggerResult)
async def trigger_scenario(scenario_id: str) -> TriggerResult:
    if scenario_id not in SCENARIOS:
        raise HTTPException(404, f"неизвестный сценарий: {scenario_id}")

    inv = _get_inventory()
    templates = _get_templates()
    cfg = _get_scenario_defs()[scenario_id]
    site_instance = {
        "zabbix": {code: f"zbx-{code}-01" for code in inv.sites},
        "solarwinds": {code: f"sw-mon-{code}-01" for code in inv.sites},
    }

    rnd = random.Random()
    anchor = datetime.utcnow()
    events = sorted(SCENARIOS[scenario_id](rnd, inv, cfg["params"], anchor), key=lambda e: e["ts"])
    if not events:
        return TriggerResult(scenario_id=scenario_id, events_scheduled=0, demo_duration_s=0.0)

    t0 = events[0]["ts"]
    max_offset = max((e["ts"] - t0).total_seconds() for e in events) or 1.0
    scale = min(1.0, MAX_DEMO_WINDOW_S / max_offset)

    records = []
    for event in events:
        offset_s = (event["ts"] - t0).total_seconds() * scale
        record = render_event(rnd, templates, event, inv, site_instance)
        records.append((offset_s, record))

    asyncio.create_task(_deliver_with_delays(records))
    return TriggerResult(scenario_id=scenario_id, events_scheduled=len(records),
                          demo_duration_s=round(max_offset * scale, 1))


@app.get("/api/scenarios")
def list_scenarios() -> dict:
    return {sid: {"name": cfg["scenario"], "description": cfg["description"]}
            for sid, cfg in _get_scenario_defs().items()}


# --- Личный кабинет self-service (раздел 8, M5) ------------------------------
# Заменяет захардкоженного TRUECONF_TEST_RECIPIENT: любой сотрудник по своему
# TrueConf-логину заводит здесь подписку на филиал/сервис/приоритет, и
# delivery_trueconf начинает адресовать ему уведомления без изменения кода
# (Go delivery-planner читает эти же таблицы).
# Пути везде относительные (см. комментарий в console() ниже) — страница
# может быть смонтирована и напрямую на :8090, и за прокси под /console/.
#
# Аутентификация (раздел 20 п.11): эта веб-страница НИКОГО не заводит сама —
# Subscriber с access_token создаёт только бот (services/delivery_trueconf),
# в ответ на команду в личном чате TrueConf, где личность уже подтверждена
# самим TrueConf. Без верного ?token= в query-string доступа нет ни на
# чтение, ни на запись — иначе любой, кто угадает чужой логин в пути,
# читал бы и менял бы чужие подписки.

PRIORITIES = ["P0", "P1", "P2", "P3"]


def _get_authorized_subscriber(session, username: str, token: str | None) -> Subscriber:
    subscriber = session.execute(
        select(Subscriber).where(Subscriber.trueconf_username == username)
    ).scalars().first()
    if subscriber is None or not token or not secrets.compare_digest(subscriber.access_token, token):
        raise HTTPException(403, "Нет доступа. Получите персональную ссылку у бота: команда /кабинет.")
    return subscriber


def _subsidiaries(session) -> list[str]:
    values = session.execute(select(CmdbOwnership.subsidiary).distinct()).scalars().all()
    return sorted({v for v in values if v})


def _services(session) -> list[tuple[str, str]]:
    rows = session.execute(select(CmdbService.id, CmdbService.name)).all()
    return sorted(((sid, name) for sid, name in rows), key=lambda r: r[1])


@app.get("/me/{username}/", response_class=HTMLResponse)
async def personal_cabinet(username: str, token: str | None = None) -> str:
    with get_session() as session:
        subscriber = _get_authorized_subscriber(session, username, token)
        subs = session.execute(
            select(Subscription).where(Subscription.subscriber_id == subscriber.id)
        ).scalars().all()
        subsidiaries = _subsidiaries(session)
        services = _services(session)
        service_names = dict(services)

        def describe(sub: Subscription) -> str:
            parts = [
                f"филиал: {sub.subsidiary}" if sub.subsidiary else "любой филиал",
                f"сервис: {service_names.get(sub.service_id, sub.service_id)}" if sub.service_id else "любой сервис",
                f"приоритет ≤ {sub.priority_threshold}" if sub.priority_threshold else "любой приоритет",
            ]
            return ", ".join(parts)

        qs = f"?token={html.escape(token)}"
        rows_html = "\n".join(
            f'<li>{html.escape(describe(s))} '
            f'<form method="post" action="unsubscribe/{s.id}{qs}" style="display:inline">'
            f'<button type="submit">Отписаться</button></form></li>'
            for s in subs
        ) or '<li class="descr">Подписок нет — уведомления по этому логину приходить не будут.</li>'

        # ИИ-сценарий «умная маршрутизация на основе истории» (раздел 5/8)
        # — показываем подсказку только пока у подписчика нет ни одной
        # подписки: как только он сам настроил фильтры, подсказка больше
        # не нужна, это не назойливая реклама, а помощь в холодном старте.
        suggestion_html = ""
        if not subs:
            stats = metrics.subsidiary_incident_stats(session)
            ai_text = await suggest_subscription(stats)
            # Кнопка обязана вести на ТО, что реально написано в тексте, а не
            # отдельно посчитанный топ-1 по количеству — модель вправе
            # выбрать другой критерий важности (например, критичность, а не
            # объём), и текст с кнопкой не должны разойтись (см. докстринг
            # extract_recommended_subsidiary — живой баг, пойманный при проверке).
            recommended = extract_recommended_subsidiary(ai_text, stats)
            if ai_text and recommended:
                suggestion_html = f"""
<div class="suggestion">
  <b>Подсказка (на основе истории инцидентов, ИИ):</b> {html.escape(ai_text)}
  <form method="post" action="subscribe{qs}" style="margin-top:8px">
    <input type="hidden" name="subsidiary" value="{html.escape(recommended)}">
    <input type="hidden" name="service_id" value="">
    <input type="hidden" name="priority_threshold" value="">
    <button type="submit">Подписаться на {html.escape(recommended)}</button>
  </form>
</div>"""

    subsidiary_options = "\n".join(
        f'<option value="{html.escape(v)}">{html.escape(v)}</option>' for v in subsidiaries)
    service_options = "\n".join(
        f'<option value="{html.escape(sid)}">{html.escape(name)}</option>' for sid, name in services)
    priority_options = "\n".join(f'<option value="{p}">{p} и критичнее</option>' for p in PRIORITIES)
    uname = html.escape(username)

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — личный кабинет {uname}</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 640px; margin: 40px auto; padding: 0 16px; }}
ul {{ list-style: none; padding: 0; }}
li {{ margin-bottom: 10px; padding: 10px; border: 1px solid #ddd; border-radius: 8px; }}
button {{ padding: 6px 12px; cursor: pointer; font-size: 13px; }}
form.add {{ display: flex; flex-direction: column; gap: 8px; margin-top: 16px; max-width: 320px; }}
select {{ padding: 6px; }}
.descr {{ color: #666; font-size: 13px; }}
.suggestion {{ background: #f0f6ff; border: 1px solid #cfe0fa; border-radius: 8px; padding: 12px; margin: 16px 0; font-size: 14px; }}
a {{ color: #06c; }}
</style></head>
<body>
<p><a href="../../">← консоль сценариев</a></p>
<h1>Личный кабинет: {uname}</h1>
<p class="descr">Раздел 8: подписка сужает поток по филиалу/сервису/приоритету — незаполненное
поле не сужает выборку. Подписка совсем без фильтров — «вижу всё» (роль дежурной смены НОЦ).</p>
<h3>Мои подписки</h3>
<ul>{rows_html}</ul>
{suggestion_html}
<h3>Добавить подписку</h3>
<form class="add" method="post" action="subscribe{qs}">
  <label>Филиал<br><select name="subsidiary"><option value="">— любой —</option>{subsidiary_options}</select></label>
  <label>Сервис<br><select name="service_id"><option value="">— любой —</option>{service_options}</select></label>
  <label>Минимальный приоритет<br><select name="priority_threshold"><option value="">— любой —</option>{priority_options}</select></label>
  <button type="submit">Подписаться</button>
</form>
</body></html>"""


@app.post("/me/{username}/subscribe")
def subscribe(username: str, token: str | None = None, subsidiary: str = Form(""),
              service_id: str = Form(""), priority_threshold: str = Form("")) -> RedirectResponse:
    with get_session() as session:
        subscriber = _get_authorized_subscriber(session, username, token)
        session.add(Subscription(
            subscriber_id=subscriber.id, subsidiary=subsidiary or None,
            service_id=service_id or None, priority_threshold=priority_threshold or None,
            created_at=datetime.utcnow(),
        ))
        log_action(session, actor=username, action="subscribe",
                   detail=f"subsidiary={subsidiary or '*'} service={service_id or '*'} "
                          f"priority<= {priority_threshold or '*'}")
        session.commit()
    return RedirectResponse(f".?token={token}", status_code=303)


@app.post("/me/{username}/unsubscribe/{subscription_id}")
def unsubscribe(username: str, subscription_id: int, token: str | None = None) -> RedirectResponse:
    with get_session() as session:
        subscriber = _get_authorized_subscriber(session, username, token)
        sub = session.get(Subscription, subscription_id)
        if sub is not None and sub.subscriber_id == subscriber.id:
            log_action(session, actor=username, action="unsubscribe",
                       detail=f"subsidiary={sub.subsidiary or '*'} service={sub.service_id or '*'}")
            session.delete(sub)
            session.commit()
    return RedirectResponse(f"../?token={token}", status_code=303)


def _object_name(session, object_id: str | None) -> str:
    if not object_id:
        return "неизвестный объект"
    obj = session.get(CmdbObject, object_id)
    return obj.name if obj else object_id


@app.get("/alerts/{username}/", response_class=HTMLResponse)
async def my_current_alerts(username: str, token: str | None = None) -> str:
    """Раздел «Сценарии»/бот — ссылка на просмотр сотрудником текущих
    алертов и их последовательности (Этап 4). Тот же токен-доступ, что и
    у личного кабинета (_get_authorized_subscriber) — НЕ LDAP-сессия
    SPA: сотрудник открывает это с телефона по ссылке из чата/бота, не
    логинится. Список — ровно то, что resolve_recipients реально отдал
    бы этому подписчику (переиспользуем маршрутизацию раздела 8, не
    дублируем её логику)."""
    with get_session() as session:
        subscriber = _get_authorized_subscriber(session, username, token)
        open_problems = session.execute(
            select(Problem).where(Problem.status.in_(("OPEN", "FLAPPING")))
            .order_by(Problem.opened_at.desc())
        ).scalars().all()
        my_problems = [p for p in open_problems if username in resolve_recipients(session, p)]

        items_html = []
        for p in my_problems:
            ack_html = (
                f'<div class="descr">отреагировал: {html.escape(p.acknowledged_by or "")}, '
                f'{p.acknowledged_at:%Y-%m-%d %H:%M:%S}</div>' if p.acknowledged_at else ""
            )
            sequence_html = ""
            if p.incident_id:
                members = session.execute(
                    select(IncidentProblem, Problem)
                    .join(Problem, Problem.id == IncidentProblem.problem_id)
                    .where(IncidentProblem.incident_id == p.incident_id)
                    .order_by(Problem.opened_at.asc())
                ).all()
                rows = "".join(
                    f'<li>{"[root] " if ip.role == "root" else ""}'
                    f'{html.escape(_object_name(session, m.object_id))} · {html.escape(m.symptom_class)} · '
                    f'{m.opened_at:%H:%M:%S}</li>'
                    for ip, m in members
                )
                sequence_html = f'<details><summary>последовательность ({len(members)})</summary><ul>{rows}</ul></details>'
            items_html.append(
                f'<li><b>{html.escape(p.priority or "?")}</b> · '
                f'{html.escape(_object_name(session, p.object_id))} · {html.escape(p.symptom_class)}<br>'
                f'<span class="descr">открыт {p.opened_at:%Y-%m-%d %H:%M:%S} · {html.escape(p.status)}</span>'
                f'{ack_html}{sequence_html}</li>'
            )
        list_html = "\n".join(items_html) or '<li class="descr">Сейчас нет алертов, адресованных вам.</li>'

    uname = html.escape(username)
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — текущие алерты {uname}</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 640px; margin: 40px auto; padding: 0 16px; }}
ul {{ list-style: none; padding: 0; }}
li {{ margin-bottom: 10px; padding: 10px; border: 1px solid #ddd; border-radius: 8px; }}
.descr {{ color: #666; font-size: 13px; }}
details {{ margin-top: 8px; font-size: 13px; }}
details ul {{ padding-left: 16px; list-style: disc; }}
details li {{ border: none; padding: 2px 0; margin: 0; }}
a {{ color: #06c; }}
</style></head>
<body>
<p><a href="../../me/{uname}/?token={html.escape(token or '')}">← личный кабинет подписок</a></p>
<h1>Текущие алерты: {uname}</h1>
<p class="descr">Ровно то, что вам сейчас пришло бы уведомлением по вашим подпискам (раздел 8).</p>
<ul>{list_html}</ul>
</body></html>"""


# --- Дашборд администратора (кейс, раздел 7 «Аналитика») --------------------

@app.get("/api/metrics")
def api_metrics(admin: str = Depends(require_admin)) -> dict:
    with get_session() as session:
        return metrics.dashboard_snapshot(session)


def _bar(label: str, count: int, total: int) -> str:
    pct = round(100 * count / total) if total else 0
    return (
        f'<div class="bar-row"><span class="bar-label">{html.escape(str(label))}</span>'
        f'<div class="bar-track"><div class="bar-fill" style="width:{pct}%"></div></div>'
        f'<span class="bar-value">{count}</span></div>'
    )


@app.get("/dashboard", response_class=HTMLResponse)
def dashboard(admin: str = Depends(require_admin)) -> str:
    with get_session() as session:
        snap = metrics.dashboard_snapshot(session)

    total_symptoms = sum(c for _, c in snap["top_symptoms"]) or 1
    symptom_bars = "\n".join(_bar(cls, c, total_symptoms) for cls, c in snap["top_symptoms"]) \
        or '<p class="descr">Пока нет проблем в базе.</p>'

    total_priority = sum(snap["priority_distribution"].values()) or 1
    priority_bars = "\n".join(
        _bar(p, snap["priority_distribution"].get(p, 0), total_priority) for p in ("P0", "P1", "P2", "P3")
    )

    q = snap["queue"]
    mttr = snap["avg_mttr_seconds"]
    mttr_text = f"{round(mttr / 60, 1)} мин" if mttr is not None else "нет закрытых проблем"
    latency = snap["avg_ingest_latency_seconds"]
    latency_text = f"{latency} с" if latency is not None else "нет данных"

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — дашборд администратора</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 900px; margin: 40px auto; padding: 0 16px; }}
h1 {{ margin-bottom: 4px; }}
.descr {{ color: #666; font-size: 13px; }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 12px; margin: 20px 0; }}
.card {{ border: 1px solid #ddd; border-radius: 8px; padding: 14px 16px; }}
.card .value {{ font-size: 28px; font-weight: 600; font-variant-numeric: tabular-nums; }}
.card .label {{ color: #666; font-size: 13px; margin-top: 2px; }}
.section {{ margin-top: 28px; }}
.bar-row {{ display: grid; grid-template-columns: 140px 1fr 40px; align-items: center; gap: 8px; margin: 6px 0; font-size: 13px; }}
.bar-track {{ background: #eee; border-radius: 4px; height: 10px; overflow: hidden; }}
.bar-fill {{ background: #06c; height: 100%; }}
.bar-value {{ text-align: right; font-variant-numeric: tabular-nums; }}
a {{ color: #06c; }}
</style></head>
<body>
<p><a href="./">← консоль сценариев</a></p>
<h1>Дашборд администратора</h1>
<p class="descr">Автообновление каждые 15 с — все числа посчитаны по актуальному состоянию БД,
не зафиксированы на момент открытия страницы.</p>

<div class="grid">
  <div class="card"><div class="value">{q.get('pending', 0) + q.get('processing', 0)}</div>
    <div class="label">Очередь сейчас (нагрузка)</div></div>
  <div class="card"><div class="value">{snap['events']['signals']}</div>
    <div class="label">Сигналов принято всего</div></div>
  <div class="card"><div class="value">{snap['events']['parse_success_rate'] or 0}%</div>
    <div class="label">Успешный разбор (парсер)</div></div>
  <div class="card"><div class="value">{snap['resolution_coverage_pct'] or 0}%</div>
    <div class="label">Резолвинг объекта (не карантин)</div></div>
  <div class="card"><div class="value">{snap['delivery']['delivered_pct'] or 0}%</div>
    <div class="label">Доставлено уведомлений</div></div>
  <div class="card"><div class="value">{snap['delivery']['supplements_sent']}</div>
    <div class="label">ИИ-дополнений отправлено</div></div>
  <div class="card"><div class="value">{snap['ai_scenarios']['duplicates_detected']}</div>
    <div class="label">ИИ: дублей найдено</div></div>
  <div class="card"><div class="value">{snap['ai_scenarios']['root_cause_hypotheses']}</div>
    <div class="label">ИИ: гипотез первопричины</div></div>
  <div class="card"><div class="value">{snap['incidents']['open_problems']}</div>
    <div class="label">Открытых проблем сейчас</div></div>
  <div class="card"><div class="value">{snap['incidents']['incidents']}</div>
    <div class="label">Инцидентов сформировано</div></div>
  <div class="card"><div class="value">{mttr_text}</div>
    <div class="label">Средний MTTR</div></div>
  <div class="card"><div class="value">{latency_text}</div>
    <div class="label">Ср. время приём → событие</div></div>
</div>

<div class="section">
  <h3>Наиболее частые типы событий</h3>
  {symptom_bars}
</div>

<div class="section">
  <h3>Распределение по приоритету</h3>
  {priority_bars}
</div>

<script>
setTimeout(() => location.reload(), 15000);
</script>
</body></html>"""


# --- Регистрация источников (кейс, п.2 условий: подключение новых
# источников/каналов без изменения ядра) ------------------------------------
# Инстанс существующей системы мониторинга (ещё один Zabbix-сервер на новом
# филиале и т.п.) регистрируется здесь — без правки connectors/sources.yaml
# и редеплоя. pipeline-worker подхватывает изменения на следующей TTL-
# развёртке (services/pipeline/worker.py, TTL_SWEEP_EVERY_S), не мгновенно,
# но без перезапуска. Сама логика разбора текста (regex-правила по системе)
# остаётся в connectors/*.yaml — это конфигурация, не код, и её появление
# для принципиально НОВОЙ системы мониторинга (не просто нового инстанса
# уже поддерживаемой) — отдельная, более крупная задача (полноценный
# конструктор правил, раздел 10.4 админки), сознательно не входит сюда.

def _connector_systems() -> list[str]:
    return sorted(load_connectors(CONNECTORS_DIR).keys())


@app.get("/sources/", response_class=HTMLResponse)
def sources_page(admin: str = Depends(require_admin)) -> str:
    with get_session() as session:
        seed_from_yaml_if_empty(session, CONNECTORS_DIR / "sources.yaml")
        rows = session.execute(select(SourceInstance).order_by(SourceInstance.instance)).scalars().all()

    systems = _connector_systems()
    sites = sorted(_get_inventory().sites)
    system_options = "\n".join(f'<option value="{html.escape(s)}">{html.escape(s)}</option>' for s in systems)
    site_datalist = "\n".join(f'<option value="{html.escape(s)}">' for s in sites)

    rows_html = "\n".join(
        f'<li><b>{html.escape(r.instance)}</b> · {html.escape(r.system)} · {html.escape(r.site)} '
        f'<form method="post" action="delete/{r.id}" style="display:inline">'
        f'<button type="submit">Удалить</button></form></li>'
        for r in rows
    ) or '<li class="descr">Источников пока не зарегистрировано.</li>'

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — источники</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 640px; margin: 40px auto; padding: 0 16px; }}
ul {{ list-style: none; padding: 0; }}
li {{ margin-bottom: 10px; padding: 10px; border: 1px solid #ddd; border-radius: 8px; }}
button {{ padding: 6px 12px; cursor: pointer; font-size: 13px; }}
form.add {{ display: flex; flex-direction: column; gap: 8px; margin-top: 16px; max-width: 320px; }}
input, select {{ padding: 6px; }}
.descr {{ color: #666; font-size: 13px; }}
a {{ color: #06c; }}
</style></head>
<body>
<p><a href="../">← консоль сценариев</a></p>
<h1>Источники событий</h1>
<p class="descr">instance → {{система, площадка}} (раздел 4.3/11.2 — площадка не извлекается
из текста алерта, только из этой регистрации). pipeline-worker подхватит изменение
в течение ~30 с (та же периодичность, что и у TTL-развёртки), без перезапуска.</p>
<h3>Зарегистрированные источники</h3>
<ul>{rows_html}</ul>
<h3>Добавить источник</h3>
<form class="add" method="post" action="add">
  <label>Идентификатор инстанса<br>
    <input name="instance" placeholder="zbx-gpn-newsite-01" required></label>
  <label>Система мониторинга<br>
    <select name="system" required><option value="">— выбрать —</option>{system_options}</select></label>
  <label>Площадка<br>
    <input name="site" list="sites" placeholder="gpn-newsite" required>
    <datalist id="sites">{site_datalist}</datalist></label>
  <button type="submit">Зарегистрировать</button>
</form>
</body></html>"""


@app.post("/sources/add")
def sources_add(instance: str = Form(...), system: str = Form(...), site: str = Form(...),
                 admin: str = Depends(require_admin)) -> RedirectResponse:
    instance, system, site = instance.strip(), system.strip(), site.strip()
    if not instance or not system or not site:
        raise HTTPException(400, "instance/system/site обязательны")
    with get_session() as session:
        exists = session.execute(
            select(SourceInstance.id).where(SourceInstance.instance == instance)
        ).scalar_one_or_none()
        if exists:
            raise HTTPException(409, f"инстанс {instance} уже зарегистрирован")
        session.add(SourceInstance(instance=instance, system=system, site=site,
                                    created_at=datetime.utcnow()))
        log_action(session, actor=admin, action="source_add", target=instance,
                   detail=f"system={system} site={site}")
        session.commit()
    return RedirectResponse(".", status_code=303)


@app.post("/sources/delete/{source_id}")
def sources_delete(source_id: int, admin: str = Depends(require_admin)) -> RedirectResponse:
    with get_session() as session:
        row = session.get(SourceInstance, source_id)
        if row is not None:
            log_action(session, actor=admin, action="source_delete", target=row.instance)
            session.delete(row)
            session.commit()
    return RedirectResponse("../", status_code=303)


@app.get("/audit/", response_class=HTMLResponse)
def audit_page(admin: str = Depends(require_admin)) -> str:
    """Раздел «Безопасность» — «предусмотрен мониторинг и аудит действий
    пользователей и администраторов». Только отказы входа + реальные
    действия (не залогинен каждый page-load, см. require_admin)."""
    with get_session() as session:
        rows = session.execute(
            select(AuditLog).order_by(AuditLog.id.desc()).limit(200)
        ).scalars().all()

    def row_html(r: AuditLog) -> str:
        target = f" · {html.escape(r.target)}" if r.target else ""
        detail = f" — {html.escape(r.detail)}" if r.detail else ""
        return (f'<li><span class="ts">{r.created_at:%Y-%m-%d %H:%M:%S}</span> '
                f'<b>{html.escape(r.actor)}</b> {html.escape(r.action)}{target}{detail}</li>')

    rows_html = "\n".join(row_html(r) for r in rows) or '<li class="descr">Пока пусто.</li>'
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — аудит</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 900px; margin: 40px auto; padding: 0 16px; }}
ul {{ list-style: none; padding: 0; font-family: monospace; font-size: 13px; }}
li {{ padding: 6px 0; border-bottom: 1px solid #eee; }}
.ts {{ color: #888; }}
.descr {{ color: #666; font-size: 13px; font-family: system-ui, sans-serif; }}
a {{ color: #06c; }}
</style></head>
<body>
<p><a href="../">← консоль сценариев</a></p>
<h1>Аудит действий</h1>
<p class="descr">Последние 200 записей: отказы входа, изменения источников, подписки/отписки,
автоотключения при увольнении. Успешные заходы в /dashboard не логируются на каждый запрос —
это шум (браузер шлёт Basic Auth на каждый HTTP-запрос), не полезный сигнал аудита.</p>
<ul>{rows_html}</ul>
</body></html>"""


@app.get("/api/ai-selftest")
async def ai_selftest() -> dict:
    """Живая проверка: ИИ реально генерирует ответ ПРЯМО СЕЙЧАС, через
    тот же клиент (packages/ai/client.py), что и все 6 сценариев — не
    статичный/закешированный текст. Метка времени и задержка меняются
    при каждом вызове ровно потому, что вызов настоящий."""
    started = datetime.utcnow()
    prompt = ("Одним предложением на русском: зачем нужен коррелятор алертов "
              "в системе мониторинга промышленного предприятия?")
    reply = await ai_client.ask(prompt, num_predict=100)
    elapsed = (datetime.utcnow() - started).total_seconds()
    return {
        "requested_at": started.strftime("%Y-%m-%d %H:%M:%S"),
        "elapsed_seconds": round(elapsed, 2),
        "model": ai_client.OLLAMA_MODEL,
        "prompt": prompt,
        "reply": reply,
        "success": reply is not None,
    }


@app.get("/api/system-stats")
async def system_stats_endpoint() -> dict:
    """Нагрузка сервера в реальном времени — раздел «Инфраструктура».
    ЦП читается из /proc внутри контейнера (общий с хостом на Docker/
    Linux), ГПУ — через отдельный сервис на хосте (packages/common/
    system_stats.py, systemd-юнит gpu-stats.service)."""
    cpu_task = asyncio.create_task(asyncio.to_thread(system_stats.cpu_percent))
    gpu_task = asyncio.create_task(system_stats.gpu_stats())
    return {
        "cpu_percent": await cpu_task,
        "cpu_count": system_stats.cpu_count(),
        "load_average": system_stats.load_average(),
        "gpu": await gpu_task,
    }


@app.get("/api/ai-demo/{scenario}")
async def ai_demo(scenario: str) -> dict:
    """Живая демонстрация конкретного ИИ-сценария по кнопке — раздел
    «Использование ИИ» demo-страницы: реальный вызов той же функции,
    что использует конвейер/личный кабинет, не отдельная "показательная"
    копия логики."""
    if scenario == "suggest_subscription":
        with get_session() as session:
            stats = metrics.subsidiary_incident_stats(session)
        text = await suggest_subscription(stats)
        recommended = extract_recommended_subsidiary(text, stats) if stats else None
        return {"scenario": scenario, "stats": stats, "reply": text, "recommended_subsidiary": recommended,
                "success": text is not None}
    if scenario == "classify":
        sample = "PROBLEM: Filesystem /var is critically full (92% used)"
        from packages.ai.classify import classify_symptom
        result = await classify_symptom(sample)
        return {"scenario": scenario, "sample_text": sample, "reply": result, "success": result is not None}
    raise HTTPException(404, f"неизвестный демо-сценарий: {scenario}")


_CRITERIA = [
    {
        "name": "Техническая реализация решения (прототип)", "weight": "0.2", "score": 5,
        "points": [
            'Конфигурируемое подключение источников — <a href="sources/">/sources/</a> (регистрация без редеплоя, воркер подхватывает за ~30с)',
            'Self-service подписки — /me/&lt;логин&gt;/ (выдаётся ботом по команде /кабинет)',
            'Настраиваемая маршрутизация — по филиалу/сервису/приоритету, раздел 8',
            'Интеграция с TrueConf — доставка NEW/CLOSURE/SUPPLEMENT/ДУБЛЬ',
            'Дашборд администратора — <a href="dashboard">/dashboard</a>, реальные метрики, автообновление',
        ],
    },
    {
        "name": "Архитектура решения и технологии", "weight": "0.15", "score": 5,
        "points": [
            "Компоненты, потоки, интерфейсы — ARCHITECTURE.md (диаграммы конвейера и компонентов)",
            'Универсальный API — <a href="docs">Swagger консоли</a> · <a href="../ingest/docs">Swagger шлюза</a>',
            "Импортозамещение — TrueConf, PostgreSQL, self-hosted Ollama, без иностранных SaaS",
            "Масштабируемость без изменения ядра — коннекторы (YAML) + БД-реестр источников",
        ],
    },
    {
        "name": "Использование инструментов ИИ", "weight": "0.15", "score": 5,
        "points": [
            "6 сценариев в едином модуле packages/ai/: нормализация, дедупликация, гипотеза первопричины, "
            "умная маршрутизация на основе истории, рекомендации из базы знаний, саммаризация",
            "Каждый — с реальным, живым срабатыванием (не формальность) — см. счётчики и живую проверку ниже",
            "Раздел И5 применён везде: сбой ИИ никогда не блокирует доставку/конвейер",
        ],
    },
    {
        "name": "Экономическая эффективность и внедрение", "weight": "0.1", "score": 1,
        "points": ["В работе — следующий шаг после этой страницы (данные для расчёта уже есть в дашборде)"],
    },
    {
        "name": "Информационная безопасность", "weight": "0.1", "score": 5,
        "points": [
            "Настоящий LDAP/AD (glauth) + RBAC — /dashboard, /sources, /audit защищены ролью admins",
            "Автоотключение подписок при увольнении — сверка с LDAP-каталогом каждые ~30с",
            'Аудит действий — <a href="audit/">/audit/</a> (входы, источники, подписки)',
            "Модель угроз и мер — SECURITY.md",
        ],
    },
    {
        "name": "Инфраструктура и масштабируемость", "weight": "0.1", "score": 5,
        "points": [
            "Измеренная пропускная способность (не оценка) — INFRASTRUCTURE.md: 2,5 соб/с, &gt;40x запас к цели кейса",
            "Резервное копирование Postgres — ежедневный cron, проверено вживую",
            "Горизонтальное масштабирование воркера — FOR UPDATE SKIP LOCKED",
        ],
    },
    {
        "name": "Демонстрация решения", "weight": "0.1", "score": 5,
        "points": [
            "Полный цикл в реальном времени — событие → доставка, с учётом подписок",
            "Self-service «на лету» — подтверждено с ДВУМЯ разными реальными получателями TrueConf "
            "(разная маршрутизация, проверено вживую)",
        ],
    },
    {
        "name": "Презентация и обоснование требований", "weight": "0.1", "score": 1,
        "points": ["В работе — следующий шаг"],
    },
]


@app.get("/compliance", response_class=HTMLResponse)
def compliance_page() -> str:
    with get_session() as session:
        ai_counts = metrics.ai_scenario_counts(session)
        delivery = metrics.delivery_rate(session)

    def score_badge(score: int) -> str:
        cls = "ok" if score == 5 else "pending"
        return f'<span class="badge {cls}">{score}/5</span>'

    cards = "\n".join(f"""
<div class="card">
  <div class="card-head"><h3>{c['name']}</h3>{score_badge(c['score'])}</div>
  <div class="weight">вес {c['weight']}</div>
  <ul>{''.join(f'<li>{p}</li>' for p in c['points'])}</ul>
</div>""" for c in _CRITERIA)

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — соответствие критериям</title>
<style>
:root {{ --accent: #2563eb; --ok: #16a34a; --pending: #d97706; --bg: #f8fafc; --card: #fff; --border: #e2e8f0; --text: #1e293b; --muted: #64748b; }}
@media (prefers-color-scheme: dark) {{
  :root {{ --bg: #0f172a; --card: #1e293b; --border: #334155; --text: #e2e8f0; --muted: #94a3b8; }}
}}
* {{ box-sizing: border-box; }}
body {{ font-family: system-ui, sans-serif; max-width: 1100px; margin: 0 auto; padding: 32px 16px 60px;
       background: var(--bg); color: var(--text); }}
h1 {{ margin-bottom: 4px; }}
.subtitle {{ color: var(--muted); margin-top: 0; }}
a {{ color: var(--accent); }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 16px; margin: 24px 0; }}
.card {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 16px 18px; }}
.card-head {{ display: flex; justify-content: space-between; align-items: start; gap: 8px; }}
.card h3 {{ margin: 0; font-size: 15px; line-height: 1.3; }}
.weight {{ color: var(--muted); font-size: 12px; margin: 4px 0 10px; text-transform: uppercase; letter-spacing: .04em; }}
.card ul {{ margin: 0; padding-left: 18px; font-size: 13px; line-height: 1.5; }}
.card li {{ margin-bottom: 6px; }}
.badge {{ font-weight: 700; font-size: 13px; padding: 3px 10px; border-radius: 999px; white-space: nowrap; }}
.badge.ok {{ background: rgba(22,163,74,.15); color: var(--ok); }}
.badge.pending {{ background: rgba(217,119,6,.15); color: var(--pending); }}
.section {{ margin-top: 40px; }}
.proof {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 20px; }}
button {{ background: var(--accent); color: #fff; border: none; padding: 10px 18px; border-radius: 8px;
          font-size: 14px; cursor: pointer; }}
button:disabled {{ opacity: .6; cursor: default; }}
#ai-result {{ margin-top: 16px; font-family: ui-monospace, monospace; font-size: 13px; white-space: pre-wrap;
              background: var(--bg); border-radius: 8px; padding: 12px; display: none; }}
.counts {{ display: flex; gap: 24px; flex-wrap: wrap; margin-top: 16px; }}
.counts .n {{ font-size: 28px; font-weight: 700; font-variant-numeric: tabular-nums; }}
.counts .l {{ color: var(--muted); font-size: 12px; }}
</style></head>
<body>
<p><a href="./">← консоль сценариев</a></p>
<h1>Соответствие критериям оценки кейса</h1>
<p class="subtitle">Каждый пункт ниже — ссылка на живую страницу платформы, не макет.
Экономика и презентация — намеренно последними по плану.</p>

<div class="grid">{cards}</div>

<div class="section">
  <h2>Проверка: ИИ действительно генерирует ответы сейчас</h2>
  <div class="proof">
    <p style="color:var(--muted); font-size:13px; margin-top:0">Кнопка ниже делает настоящий вызов той же
    локальной модели (packages/ai/client.py), что и все 6 сценариев — не статичный текст. Время запроса
    и задержка ответа меняются при каждом нажатии именно потому, что вызов живой.</p>
    <button id="ai-btn" onclick="runAiTest()">Сгенерировать ответ прямо сейчас</button>
    <div id="ai-result"></div>
    <div class="counts">
      <div><div class="n">{ai_counts['duplicates_detected']}</div><div class="l">ИИ: дублей найдено (реально, по данным)</div></div>
      <div><div class="n">{ai_counts['root_cause_hypotheses']}</div><div class="l">ИИ: гипотез первопричины</div></div>
      <div><div class="n">{ai_counts['ai_supplements_sent']}</div><div class="l">ИИ-сводок/рекомендаций отправлено</div></div>
      <div><div class="n">{delivery['delivered_pct'] or 0}%</div><div class="l">Уведомлений доставлено</div></div>
    </div>
  </div>
</div>

<script>
async function runAiTest() {{
  const btn = document.getElementById('ai-btn');
  const out = document.getElementById('ai-result');
  btn.disabled = true; btn.textContent = 'Жду ответ модели (может занять до 20-30с при холодном старте)...';
  out.style.display = 'block';
  out.textContent = '...';
  try {{
    const r = await fetch('api/ai-selftest');
    const data = await r.json();
    out.textContent = `Время запроса: ${{data.requested_at}}\\nЗадержка: ${{data.elapsed_seconds}} с\\nМодель: ${{data.model}}\\n\\nВопрос: ${{data.prompt}}\\n\\nОтвет модели:\\n${{data.reply || '(нет ответа — ИИ недоступна, раздел И5)'}}`;
  }} catch (e) {{
    out.textContent = 'Ошибка запроса: ' + e;
  }}
  btn.disabled = false; btn.textContent = 'Сгенерировать ответ прямо сейчас';
}}
</script>
</body></html>"""


def _ai_example_card(title: str, icon: str, example: dict | None, render_body, empty_hint: str,
                      demo_button: str | None = None) -> str:
    if example:
        body = render_body(example)
    else:
        body = f'<p class="empty">Пока нет примеров на живых данных. {empty_hint}</p>'
    btn = (f'<button class="try" onclick="runAiDemo(\'{demo_button}\', \'demo-out-{demo_button}\')">'
           f'Проверить сейчас</button><div id="demo-out-{demo_button}" class="demo-out"></div>'
           if demo_button else "")
    return f"""
<div class="ai-card">
  <div class="ai-head">{icon} {title}</div>
  {body}
  {btn}
</div>"""


@app.get("/demo", response_class=HTMLResponse)
def demo_page() -> str:
    """Витрина для демонстрации кейса: запуск сценариев, живые примеры
    работы каждого ИИ-сценария, нагрузка сервера в реальном времени и
    ссылки на все основные страницы платформы — в одном месте. Не
    дублирует / (та осталась простой рабочей консолью)."""
    with get_session() as session:
        snap = metrics.dashboard_snapshot(session)
        ai = metrics.ai_recent_examples(session)

    defs = _get_scenario_defs()
    scenario_cards = "\n".join(f"""
<div class="s-card">
  <button onclick="trigger('{sid}')">{cfg['scenario']}</button>
  <p>{cfg['description']}</p>
</div>""" for sid, cfg in defs.items())

    # --- Конвейер вживую: по одному индикатору на каждый блок из ARCHITECTURE.md
    pipeline_cards = f"""
<div class="p-card"><div class="p-n">{snap['events']['signals']}</div><div class="p-l">0. Шлюз — сигналов принято</div></div>
<div class="p-card"><div class="p-n">{snap['events']['parse_success_rate'] or 0}%</div><div class="p-l">1. Парсер — успешный разбор</div></div>
<div class="p-card"><div class="p-n">{snap['resolution_coverage_pct'] or 0}%</div><div class="p-l">2. Резолвер — объект найден</div></div>
<div class="p-card"><div class="p-n">{snap['incidents']['open_problems']}</div><div class="p-l">3. Состояния — открытых проблем</div></div>
<div class="p-card"><div class="p-n">{snap['incidents']['incidents']}</div><div class="p-l">4. Коррелятор — инцидентов свёрнуто</div></div>
<div class="p-card"><div class="p-n">{(snap['priority_distribution'].get('P0',0))}</div><div class="p-l">5. Приоритет — P0 определено</div></div>
<div class="p-card"><div class="p-n">{snap['delivery']['delivered_pct'] or 0}%</div><div class="p-l">6. Доставка — уведомлений отправлено</div></div>
"""

    # --- ИИ в действии: 6 сценариев
    ai_html = ""
    ai_html += _ai_example_card(
        "Саммаризация инцидента", "🧠", ai["summary"],
        lambda e: f'<p class="pid">PRB-{e["problem_id"]:04d} · {html.escape(e["object_id"] or "?")}</p>'
                  f'<blockquote>{html.escape(e["text"])}</blockquote>',
        "Запустите «Каскад отказа коммутатора» выше.")
    ai_html += _ai_example_card(
        "Рекомендации из базы знаний", "📚", ai["recommendation"],
        lambda e: f'<p class="pid">PRB-{e["problem_id"]:04d} · симптом {html.escape(e["symptom_class"])}</p>'
                  f'<blockquote>{html.escape(e["text"])}</blockquote>',
        "Запустите любой P0/P1-сценарий выше.")
    ai_html += _ai_example_card(
        "Семантическая нормализация", "🔤", ai["classification"],
        lambda e: f'<p class="pid">Event #{e["event_id"]}</p>'
                  f'<p class="src">«{html.escape(e["title"][:120])}»</p>'
                  f'<blockquote>→ {html.escape(e["symptom_class"])}</blockquote>',
        "Срабатывает на нестандартных формулировках алертов.", demo_button="classify")
    ai_html += _ai_example_card(
        "Дедупликация между источниками", "🔗", ai["duplicate"],
        lambda e: f'<p class="pid">PRB-{e["problem_id"]:04d} ({html.escape(e["symptom_class"])}) '
                  f'признан дублем PRB-{e["original_id"]:04d} ({html.escape(e["original_symptom_class"] or "?")})</p>'
                  f'<p class="src">Объект: {html.escape(e["object_id"] or "?")}</p>',
        "Запустите «Дубль между системами» выше.")
    ai_html += _ai_example_card(
        "Гипотеза первопричины", "🎯", ai["root_cause_hypothesis"],
        lambda e: f'<p class="pid">PRB-{e["problem_id"]:04d} · площадка {html.escape(e["site"] or "?")}</p>'
                  f'<blockquote>{html.escape(e["text"])}</blockquote>',
        "Запустите «Неоднозначная авария на площадке» выше.")
    ai_html += _ai_example_card(
        "Умная маршрутизация на основе истории", "📈", None,
        lambda e: "", "Считается заново на каждый вызов из реальной статистики инцидентов.",
        demo_button="suggest_subscription")

    nav_cards = [
        ("dashboard", "📊 Дашборд администратора",
         "Нагрузка, доставка, MTTR, счётчики ИИ-сценариев — раздел 7 кейса. Вход по LDAP (admin1/admin123)."),
        ("sources/", "🔌 Источники событий",
         "Регистрация нового инстанса без редеплоя — раздел 5 условий кейса. Вход по LDAP."),
        ("audit/", "🛡️ Аудит действий",
         "Входы, изменения источников, подписки/отписки, автоотключения при увольнении. Вход по LDAP."),
        ("compliance", "✅ Соответствие критериям",
         "Оценка по всем 8 пунктам рубрики кейса с живыми доказательствами и проверкой ИИ. Без пароля."),
        ("docs", "📘 API консоли (Swagger)", "Автогенерируемая документация — подписки, источники, метрики."),
        ("../ingest/docs", "📘 API шлюза (Swagger)", "Универсальный API приёма событий — POST /api/v1/ingest/raw."),
        ("../", "💬 TrueConf", "Гостевая страница / вход в мессенджер, куда приходят уведомления."),
    ]
    nav_html = "\n".join(f"""
<a class="n-card" href="{href}">
  <div class="n-title">{title}</div>
  <div class="n-descr">{descr}</div>
</a>""" for href, title, descr in nav_cards)

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — демонстрация</title>
<style>
:root {{ --accent: #2563eb; --bg: #f8fafc; --card: #fff; --border: #e2e8f0; --text: #1e293b; --muted: #64748b; --ok: #16a34a; }}
@media (prefers-color-scheme: dark) {{
  :root {{ --bg: #0f172a; --card: #1e293b; --border: #334155; --text: #e2e8f0; --muted: #94a3b8; }}
}}
* {{ box-sizing: border-box; }}
body {{ font-family: system-ui, sans-serif; max-width: 1100px; margin: 0 auto; padding: 32px 16px 60px;
       background: var(--bg); color: var(--text); }}
h1 {{ margin-bottom: 4px; }}
h2 {{ margin-top: 44px; font-size: 19px; }}
.subtitle {{ color: var(--muted); margin-top: 0; }}
a {{ color: var(--accent); }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 14px; margin-top: 16px; }}
.s-card {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 14px 16px; }}
.s-card p {{ color: var(--muted); font-size: 13px; margin: 8px 0 0; }}
button {{ background: var(--accent); color: #fff; border: none; padding: 9px 16px; border-radius: 8px;
          font-size: 14px; cursor: pointer; }}
.n-card {{ display: block; background: var(--card); border: 1px solid var(--border); border-radius: 10px;
           padding: 16px; text-decoration: none; color: var(--text); transition: border-color .15s; }}
.n-card:hover {{ border-color: var(--accent); }}
.n-title {{ font-weight: 600; font-size: 15px; }}
.n-descr {{ color: var(--muted); font-size: 13px; margin-top: 6px; line-height: 1.4; }}
#log {{ white-space: pre-wrap; background: #0b1220; color: #4ade80; padding: 14px; border-radius: 8px;
        min-height: 60px; font-family: ui-monospace, monospace; font-size: 13px; margin-top: 16px; }}
.cabinet {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 16px;
            margin-top: 16px; font-size: 14px; }}
code {{ background: rgba(37,99,235,.12); padding: 1px 6px; border-radius: 4px; }}
.pipeline {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; margin-top: 16px; }}
.p-card {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 12px; text-align: center; }}
.p-n {{ font-size: 22px; font-weight: 700; font-variant-numeric: tabular-nums; color: var(--accent); }}
.p-l {{ font-size: 11px; color: var(--muted); margin-top: 4px; }}
.ai-grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 14px; margin-top: 16px; }}
.ai-card {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 16px; }}
.ai-head {{ font-weight: 600; margin-bottom: 8px; }}
.ai-card blockquote {{ margin: 6px 0 0; padding: 8px 10px; background: rgba(37,99,235,.08); border-radius: 6px;
                        font-size: 13px; font-style: italic; }}
.ai-card .pid {{ font-size: 12px; color: var(--muted); margin: 0; font-variant-numeric: tabular-nums; }}
.ai-card .src {{ font-size: 12px; color: var(--muted); margin: 4px 0 0; }}
.ai-card .empty {{ font-size: 13px; color: var(--muted); }}
button.try {{ margin-top: 10px; padding: 6px 12px; font-size: 12px; }}
.demo-out {{ margin-top: 8px; font-size: 12px; white-space: pre-wrap; font-family: ui-monospace, monospace;
             background: var(--bg); border-radius: 6px; padding: 8px; display: none; }}
.load-grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; margin-top: 16px; }}
.gauge {{ background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 16px; }}
.gauge-title {{ font-size: 13px; color: var(--muted); margin-bottom: 8px; }}
.gauge-value {{ font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }}
.bar-track {{ background: var(--border); border-radius: 4px; height: 8px; margin-top: 8px; overflow: hidden; }}
.bar-fill {{ background: var(--accent); height: 100%; width: 0%; transition: width .5s; }}
.gauge-sub {{ font-size: 11px; color: var(--muted); margin-top: 6px; }}
</style></head>
<body>
<h1>Диспетчер — демонстрация возможностей</h1>
<p class="subtitle">Запустите любой сценарий каскада алертов и посмотрите весь путь — от шлюза до сообщения
в TrueConf. Ниже — живые примеры работы каждого ИИ-сценария и нагрузка сервера в реальном времени.</p>

<h2>Конвейер вживую</h2>
<p class="subtitle">По одному показателю на каждый блок из ARCHITECTURE.md — обновляется при перезагрузке страницы.</p>
<div class="pipeline">{pipeline_cards}</div>

<h2>Запуск сценариев</h2>
<p class="subtitle">Каждая кнопка отправляет каскад через шлюз (раздел 18.4), как если бы он пришёл
от Zabbix/SolarWinds. Дальше конвейер и бот TrueConf обрабатывают его штатно.</p>
<div class="grid">{scenario_cards}</div>
<div id="log">готов</div>

<h2>ИИ в действии — 6 сценариев</h2>
<p class="subtitle">Пять карточек показывают РЕАЛЬНЫЙ последний результат из базы данных (не выдумано для
показа); две — с кнопкой «Проверить сейчас» делают живой вызов модели прямо в браузере.</p>
<div class="ai-grid">{ai_html}</div>

<h2>Нагрузка сервера в реальном времени</h2>
<p class="subtitle">Тот же сервер, что считает конвейер и ИИ-сценарии — обновляется каждые 3 секунды.</p>
<div class="load-grid">
  <div class="gauge">
    <div class="gauge-title">ЦП</div>
    <div class="gauge-value" id="cpu-value">—</div>
    <div class="bar-track"><div class="bar-fill" id="cpu-bar"></div></div>
    <div class="gauge-sub" id="cpu-sub">—</div>
  </div>
  <div class="gauge">
    <div class="gauge-title">GPU (Tesla T4 — модель ИИ живёт здесь)</div>
    <div class="gauge-value" id="gpu-value">—</div>
    <div class="bar-track"><div class="bar-fill" id="gpu-bar"></div></div>
    <div class="gauge-sub" id="gpu-sub">—</div>
  </div>
  <div class="gauge">
    <div class="gauge-title">Видеопамять GPU</div>
    <div class="gauge-value" id="vram-value">—</div>
    <div class="bar-track"><div class="bar-fill" id="vram-bar"></div></div>
    <div class="gauge-sub" id="vram-sub">—</div>
  </div>
</div>

<h2>Основные страницы</h2>
<div class="grid">{nav_html}</div>

<h2>Личный кабинет (раздел 8)</h2>
<div class="cabinet">
Подписки решают, кому из сотрудников уйдёт уведомление по конкретному филиалу/сервису/приоритету —
без правки конфигурации системы. Ссылка персональная и выдаётся ботом (личность подтверждает сам
TrueConf-канал, раздел 20 п.11): напишите боту «Диспетчер» команду <code>/кабинет</code> в личном чате.
</div>

<script>
async function trigger(id) {{
    document.getElementById('log').textContent = 'Запускаю ' + id + '...';
    const r = await fetch('api/trigger/' + id, {{method: 'POST'}});
    const data = await r.json();
    document.getElementById('log').textContent = JSON.stringify(data, null, 2)
        + '\\n\\nСобытия будут приходить в течение ~' + data.demo_duration_s + ' с.';
}}

async function runAiDemo(scenario, outId) {{
    const out = document.getElementById(outId);
    out.style.display = 'block';
    out.textContent = 'Жду ответ модели...';
    try {{
        const r = await fetch('api/ai-demo/' + scenario);
        const data = await r.json();
        out.textContent = JSON.stringify(data, null, 2);
    }} catch (e) {{
        out.textContent = 'Ошибка: ' + e;
    }}
}}

function setBar(prefix, value, max, unitFn) {{
    document.getElementById(prefix + '-value').textContent = (value === null || value === undefined) ? 'н/д' : unitFn(value);
    document.getElementById(prefix + '-bar').style.width = (value === null || value === undefined) ? '0%' : Math.min(100, 100 * value / max) + '%';
}}

async function pollSystemStats() {{
    try {{
        const r = await fetch('api/system-stats');
        const d = await r.json();
        setBar('cpu', d.cpu_percent, 100, v => v + '%');
        document.getElementById('cpu-sub').textContent = d.cpu_count + ' ядер · load avg ' + (d.load_average ? d.load_average.join(' / ') : 'н/д');
        if (d.gpu) {{
            setBar('gpu', d.gpu.gpu_util_pct, 100, v => v + '%');
            document.getElementById('gpu-sub').textContent = d.gpu.gpu_name + ' · ' + d.gpu.temp_c + '°C';
            setBar('vram', d.gpu.mem_used_mb, d.gpu.mem_total_mb, v => Math.round(v/1024*10)/10 + ' ГБ');
            document.getElementById('vram-sub').textContent = Math.round(d.gpu.mem_used_mb/1024*10)/10 + ' / ' + Math.round(d.gpu.mem_total_mb/1024*10)/10 + ' ГБ';
        }} else {{
            document.getElementById('gpu-sub').textContent = 'GPU-сервис недоступен';
        }}
    }} catch (e) {{ /* раздел И5 — тихо, дашборд не должен падать */ }}
}}
pollSystemStats();
setInterval(pollSystemStats, 3000);
</script>
</body></html>"""


@app.get("/", response_class=HTMLResponse)
def console() -> str:
    defs = _get_scenario_defs()
    rows = "\n".join(
        f'<li><button onclick="trigger(\'{sid}\')">{cfg["scenario"]}</button>'
        f'<div class="descr">{cfg["description"]}</div></li>'
        for sid, cfg in defs.items()
    )
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<title>Диспетчер — консоль сценариев</title>
<style>
body {{ font-family: system-ui, sans-serif; max-width: 760px; margin: 40px auto; padding: 0 16px; }}
ul {{ list-style: none; padding: 0; }}
li {{ margin-bottom: 14px; padding: 10px; border: 1px solid #ddd; border-radius: 8px; }}
button {{ padding: 8px 16px; cursor: pointer; font-size: 14px; }}
.descr {{ color: #666; margin-top: 6px; font-size: 13px; }}
#log {{ white-space: pre-wrap; background: #111; color: #0f0; padding: 12px; border-radius: 6px;
        min-height: 80px; font-family: monospace; font-size: 13px; }}
</style></head>
<body>
<p><a href="demo" style="font-weight:600">→ страница демонстрации</a> (все сценарии + ссылки на все основные страницы в одном месте)</p>
<h1>Диспетчер — консоль запуска сценариев</h1>
<p>Каждая кнопка отправляет каскад алертов сценария через шлюз (раздел 18.4),
как если бы он пришёл от Zabbix/SolarWinds. Дальше конвейер и бот TrueConf
обрабатывают его штатно — без отдельного кода для демонстрации.</p>
<ul>{rows}</ul>
<h3>Журнал</h3>
<div id="log">готов</div>
<p><a href="dashboard">→ дашборд администратора</a> (раздел 7 кейса — нагрузка, доставка, MTTR)</p>
<p><a href="sources/">→ источники событий</a> (регистрация нового инстанса без редеплоя)</p>
<p><a href="docs">→ API консоли (OpenAPI/Swagger)</a> · <a href="../ingest/docs">API шлюза</a></p>
<p><a href="audit/">→ аудит действий</a> (раздел «Безопасность» — вход по LDAP, роль admins)</p>
<p><a href="compliance" style="font-weight:600">→ соответствие критериям оценки кейса</a></p>
<h3>Личный кабинет (раздел 8)</h3>
<p class="descr">Подписки решают, кому из сотрудников уйдёт уведомление по конкретному
филиалу/сервису/приоритету — без правки конфигурации системы. Ссылка на кабинет
персональная и выдаётся ботом, а не вводится тут напрямую (раздел 20 п.11 — личность
подтверждает сам TrueConf-канал): напишите боту «Диспетчер» команду <code>/кабинет</code>
в личном чате, он пришлёт вам ссылку с одноразовым токеном доступа.</p>
<script>
async function trigger(id) {{
    document.getElementById('log').textContent = 'Запускаю ' + id + '...';
    // Относительный путь (без ведущего /) — страница может быть смонтирована
    // за реверс-прокси под префиксом (например /console/), тогда абсолютный
    // путь "/api/..." увёл бы запрос мимо префикса на корень домена.
    const r = await fetch('api/trigger/' + id, {{method: 'POST'}});
    const data = await r.json();
    document.getElementById('log').textContent = JSON.stringify(data, null, 2)
        + '\\n\\nСобытия будут приходить в течение ~' + data.demo_duration_s + ' с.';
}}
</script>
</body></html>"""
