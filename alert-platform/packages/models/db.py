"""ORM-схема стадий 0-1 (раздел 3.2, 5). Два разных стола ролей:

- Signal — WORM: пишется один раз шлюзом (стадия 0), после вставки
  приложение никогда не выполняет UPDATE/DELETE над этой таблицей.
- SignalQueueEntry — очередь на PostgreSQL (раздел 15.1, демо-стенд):
  мутабельная строка бухгалтерии обработки, отдельная от WORM-сигнала,
  чтобы неизменность Signal не нарушалась служебными полями обработки.
- Event — результат успешного разбора (стадия 1). Если парсер не
  справился, Event не создаётся: сигнал остаётся в WORM (раздел И3 —
  ничего не исчезает бесследно), а SignalQueueEntry.status='parse_failed'
  с текстом ошибки — видно на дашборде (раздел 14.5) и подлежит доразбору.
"""
from __future__ import annotations

from datetime import date, datetime

from sqlalchemy import (Boolean, CheckConstraint, Date, DateTime, ForeignKey, Integer,
                         String, Text, UniqueConstraint)
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship


class Base(DeclarativeBase):
    pass


class Signal(Base):
    __tablename__ = "signals"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    source_system: Mapped[str] = mapped_column(String(64))
    source_instance: Mapped[str] = mapped_column(String(128))
    external_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    received_at: Mapped[datetime] = mapped_column(DateTime)
    raw_body: Mapped[str] = mapped_column(Text)
    hash: Mapped[str] = mapped_column(String(64))

    __table_args__ = (UniqueConstraint("source_instance", "hash", name="uq_signal_dedup"),)


class SignalQueueEntry(Base):
    __tablename__ = "signal_queue"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    signal_id: Mapped[int] = mapped_column(ForeignKey("signals.id"))
    status: Mapped[str] = mapped_column(String(32), default="pending")  # pending|done|parse_failed
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    attempts: Mapped[int] = mapped_column(Integer, default=0)
    enqueued_at: Mapped[datetime] = mapped_column(DateTime)
    processed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)

    signal: Mapped["Signal"] = relationship()


class Event(Base):
    __tablename__ = "events"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    signal_id: Mapped[int] = mapped_column(ForeignKey("signals.id"))
    dedup_key: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    state: Mapped[str] = mapped_column(String(16))                # firing | resolved
    occurred_at: Mapped[datetime] = mapped_column(DateTime)
    ingest_ts: Mapped[datetime] = mapped_column(DateTime)
    symptom_class: Mapped[str] = mapped_column(String(64))
    severity_raw: Mapped[str | None] = mapped_column(String(64), nullable=True)
    title: Mapped[str] = mapped_column(Text)
    site: Mapped[str | None] = mapped_column(String(64), nullable=True)
    object_name_raw: Mapped[str | None] = mapped_column(String(256), nullable=True)
    ip_raw: Mapped[str | None] = mapped_column(String(64), nullable=True)
    component: Mapped[str | None] = mapped_column(String(128), nullable=True)
    parser_version: Mapped[int | None] = mapped_column(Integer, nullable=True)
    resolved: Mapped[bool] = mapped_column(Boolean, default=False)  # раздел 4.3 — прошёл резолвер?
    object_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    resolution_method: Mapped[str | None] = mapped_column(String(32), nullable=True)
    # alias | fqdn | ip | fuzzy | quarantine (раздел 4.3, каскад из 6 шагов)
    resolution_confidence: Mapped[float | None] = mapped_column(nullable=True)
    problem_id: Mapped[int | None] = mapped_column(ForeignKey("problems.id"), nullable=True, index=True)
    # раздел И4: у каждого события — трассировка вплоть до проблемы, в которую оно свёрнуто
    symptom_class_source: Mapped[str | None] = mapped_column(String(16), nullable=True)
    # "rule" | "ai" — ИИ-сценарий «семантическая нормализация» (раздел 5,
    # packages/ai/classify.py). NULL для событий, которые ещё обработаны
    # версией конвейера до этой возможности — не путать с "rule" (там
    # regex справился сам).

    signal: Mapped["Signal"] = relationship()


class Problem(Base):
    """Раздел 3.2, 5 (стадия 3). Свёртка событий по dedup_key.

    Один dedup_key может пройти несколько эпизодов OPEN→RESOLVED за время
    жизни платформы — это разные строки Problem. Но если новое firing
    приходит на том же dedup_key вскоре после resolved (в пределах окна
    флаппинга), существующая строка переоткрывается, а не создаётся новая
    — иначе счётчик переключений (toggle_count) невозможно вести и
    флаппинг (раздел 5) не детектируется через границы эпизодов.
    """
    __tablename__ = "problems"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    dedup_key: Mapped[str] = mapped_column(String(64), index=True)
    status: Mapped[str] = mapped_column(String(16), default="OPEN")
    # OPEN | ACKNOWLEDGED | RESOLVED | FLAPPING | SUPPRESSED
    object_id: Mapped[str | None] = mapped_column(String(256), nullable=True)
    component: Mapped[str | None] = mapped_column(String(128), nullable=True)
    symptom_class: Mapped[str] = mapped_column(String(64))
    site: Mapped[str | None] = mapped_column(String(64), nullable=True)
    opened_at: Mapped[datetime] = mapped_column(DateTime)
    resolved_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    last_seen_at: Mapped[datetime] = mapped_column(DateTime)
    repeat_count: Mapped[int] = mapped_column(Integer, default=1)
    toggle_count: Mapped[int] = mapped_column(Integer, default=0)
    closed_by_reconciliation: Mapped[bool] = mapped_column(Boolean, default=False)
    # раздел 5: TTL-закрытие вместо цикла сверки для источников без API —
    # отдельный флаг, чтобы не искажать MTTR настоящими подтверждениями
    priority: Mapped[str | None] = mapped_column(String(8), nullable=True)  # P0-P3, раздел 7
    priority_breakdown: Mapped[str | None] = mapped_column(Text, nullable=True)
    # JSON: технический+бизнес показатель, ячейка матрицы, применённые
    # модификаторы — раздел 7: "приоритет раскрывается до ячейки матрицы"
    incident_id: Mapped[int | None] = mapped_column(ForeignKey("incidents.id"), nullable=True, index=True)
    duplicate_of_problem_id: Mapped[int | None] = mapped_column(ForeignKey("problems.id"), nullable=True)
    # ИИ-сценарий «определение дублей между источниками» (раздел 4.1,
    # packages/ai/dedup.py) — тот же реальный объект, другой symptom_class
    # у ДРУГОГО источника, не пойман точным dedup_key. Заполняется
    # worker'ом сразу после создания Problem; если не None, delivery не
    # шлёт отдельный NEW, только короткую пометку в ту же ветку переписки.
    ai_root_cause_hypothesis: Mapped[str | None] = mapped_column(Text, nullable=True)
    # ИИ-сценарий «вероятная первопричина при неоднозначной корреляции»
    # (раздел 6.1, packages/ai/root_cause_hypothesis.py) — правило-
    # ориентированный коррелятор не нашёл причинно-следственной связи
    # (например, раздел 18.4 site_outage_ambiguous: два кандидата на
    # первопричину одной площадки открылись синхронно), гипотеза ИИ
    # добавляется к тексту NEW-уведомления, явно маркированная как
    # предположение — раздел 13, структура Incident/Problem не меняется.
    acknowledged_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    acknowledged_by: Mapped[str | None] = mapped_column(String(128), nullable=True)
    # Раздел «Сценарии», Этап 3 — факт реакции на инцидент: любой ответ
    # (reply) в TrueConf на NEW-уведомление (packages/common/ack.py).
    # Осознанно НЕ трогаем status/ACKNOWLEDGED (используется как активный
    # статус в state_manager/correlator, но нигде не проставляется) —
    # иначе пришлось бы чинить все фильтры по ("OPEN","FLAPPING") в
    # delivery_trueconf, риск тихо остановить доставку. Первый ответ
    # побеждает, повторные не перезаписывают.


class CorrelationRule(Base):
    """Раздел 6.4 — конструктор правил корреляции, не алгоритм.
    Жизненный цикл draft→simulation→shadow→active→disabled (упрощённо:
    в этой версии активны только правила status='active', симулятор и
    теневой режим — раздел 10.4 админки, не реализованы)."""
    __tablename__ = "correlation_rules"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    rule_id: Mapped[str] = mapped_column(String(64), unique=True)
    name: Mapped[str] = mapped_column(String(256))
    trigger_symptom: Mapped[str] = mapped_column(String(64))
    cause_symptom: Mapped[str] = mapped_column(String(64))
    match_axes: Mapped[str] = mapped_column(String(128))  # csv: subnet,site,topology
    window_s: Mapped[int] = mapped_column(Integer)
    status: Mapped[str] = mapped_column(String(16), default="active")
    author: Mapped[str | None] = mapped_column(String(128), nullable=True)
    created_from: Mapped[str | None] = mapped_column(String(64), nullable=True)


class Incident(Base):
    """Раздел 3.2 — кластер проблем с одной первопричиной."""
    __tablename__ = "incidents"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    root_problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"))
    priority: Mapped[str | None] = mapped_column(String(8), nullable=True)
    opened_at: Mapped[datetime] = mapped_column(DateTime)
    closed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class IncidentProblem(Base):
    """Полный состав инцидента: root_problem_id — денормализованное
    удобство, здесь — источник истины о членстве (раздел 3.2)."""
    __tablename__ = "incident_problems"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    incident_id: Mapped[int] = mapped_column(ForeignKey("incidents.id"))
    problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"))
    role: Mapped[str] = mapped_column(String(16))  # root | symptom
    rule_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    # правило, сработавшее для symptom (раздел И4 — трассировка "по какому правилу")


class Notification(Base):
    """Раздел 3.2, 9.8 — акт доставки. Исходящая очередь: запись со
    status='queued' создаётся ДО отправки, отправка без записи запрещена
    (раздел 9.8 п.1). Уникальность (problem_id, type) — идемпотентность
    (9.8 п.2): повторный проход воркера не создаёт вторую отправку.
    message_id — критическое поле (раздел 3.2): по нему сопоставляются
    входящие ответы пользователя с проблемой (раздел 9.4, M8 — пока не
    реализовано, здесь только сохраняем ключ для будущего использования).
    """
    __tablename__ = "notifications"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"), index=True)
    type: Mapped[str] = mapped_column(String(16))
    # NEW | SUPPLEMENT | REPEAT | ESCALATION | CLOSURE | SCENARIO | SLA_BREACH
    recipient: Mapped[str | None] = mapped_column(String(256), nullable=True)
    # Стабильная доменная идентичность адресата. В отличие от chat_id не
    # заменяется после ответа провайдера и держит идемпотентность planner.
    chat_id: Mapped[str] = mapped_column(String(128))
    message_id: Mapped[str | None] = mapped_column(String(128), nullable=True)
    status: Mapped[str] = mapped_column(String(16), default="queued")  # queued | sent | failed
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime)
    sent_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    ai_summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    ai_recommendation: Mapped[str | None] = mapped_column(Text, nullable=True)
    # Раздел 13 — ИИ-сценарии саммаризации/рекомендаций (packages/ai/)
    # реально отправленный текст раньше был виден только в чате TrueConf;
    # сохраняем здесь же, чтобы demo-страница могла показать РЕАЛЬНЫЙ
    # последний пример, не выдуманный, читая свою же БД.

    # (problem_id, type) больше не уникально само по себе: раздел 8 (M5) —
    # одна Problem теперь может уходить нескольким подписчикам в разные
    # личные чаты, идемпотентность держится per-получатель через chat_id.
    __table_args__ = (UniqueConstraint("problem_id", "type", "chat_id", name="uq_notification_problem_type_chat"),)


class DeliveryOutbox(Base):
    """Версионированная команда каналу доставки.

    Бизнес-логика создаёт Notification и DeliveryOutbox в одной транзакции.
    Адаптер канала владеет только доставкой и обновлением provider-полей;
    он не читает Problem/Incident/CMDB и не принимает решений об адресатах.
    """
    __tablename__ = "delivery_outbox"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    notification_id: Mapped[int] = mapped_column(
        ForeignKey("notifications.id"), unique=True, index=True
    )
    contract_version: Mapped[int] = mapped_column(Integer, default=1)
    channel: Mapped[str] = mapped_column(String(32), default="trueconf", index=True)
    idempotency_key: Mapped[str] = mapped_column(String(256), unique=True)
    recipient: Mapped[str | None] = mapped_column(String(256), nullable=True)
    provider_chat_id: Mapped[str | None] = mapped_column(String(128), nullable=True)
    reply_to_notification_id: Mapped[int | None] = mapped_column(
        ForeignKey("notifications.id"), nullable=True
    )
    text: Mapped[str] = mapped_column(Text)
    parse_mode: Mapped[str] = mapped_column(String(16), default="HTML")
    status: Mapped[str] = mapped_column(String(16), default="pending", index=True)
    # pending | processing | sent | failed
    attempts: Mapped[int] = mapped_column(Integer, default=0)
    available_at: Mapped[datetime] = mapped_column(DateTime, index=True)
    claimed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    last_error: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime)
    sent_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)

    notification: Mapped["Notification"] = relationship(
        foreign_keys=[notification_id]
    )

    __table_args__ = (
        CheckConstraint(
            "recipient IS NOT NULL OR provider_chat_id IS NOT NULL",
            name="ck_delivery_outbox_target",
        ),
    )


class AiAnalysisRequest(Base):
    """Запрос на ИИ-разбор алерта по инициативе сотрудника (reply в
    TrueConf со словом-триггером). Python только фиксирует факт запроса —
    сам разбор (сбор связанных алертов, вызов Ollama, формирование и
    отправка ответа) делает Go delivery-planner, тот же узкий
    факт-контракт, что уже есть у mark_acknowledged (packages/common/ack.py)."""
    __tablename__ = "ai_analysis_requests"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"), index=True)
    requested_by: Mapped[str] = mapped_column(String(128))
    reply_to_notification_id: Mapped[int] = mapped_column(ForeignKey("notifications.id"))
    status: Mapped[str] = mapped_column(String(16), default="pending", index=True)
    # pending | done
    created_at: Mapped[datetime] = mapped_column(DateTime)
    processed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


# --- Справочники CMDB (раздел 4, 11.2) --------------------------------------
# Наполняются из того же datagen-инвентаря, что и генератор алертов
# (scripts/seed_demo.py) — единый источник истины для резолвера и для
# ground_truth генератора.

class CmdbObject(Base):
    __tablename__ = "cmdb_objects"

    id: Mapped[str] = mapped_column(String(256), primary_key=True)  # канонический object_id
    kind: Mapped[str] = mapped_column(String(32))                   # server | controller | switch
    site: Mapped[str] = mapped_column(String(64), index=True)
    name: Mapped[str] = mapped_column(String(256))
    fqdn: Mapped[str | None] = mapped_column(String(256), nullable=True)
    ip: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    inventory_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    subnet: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    # раздел 6.2, ось "подсеть" — массовая недоступность в одном сегменте
    parent_switch_id: Mapped[str | None] = mapped_column(String(256), nullable=True, index=True)
    # раздел 6.2, ось "топология": access-switch для хоста, core-switch для access-switch
    equipment_type: Mapped[str | None] = mapped_column(String(64), nullable=True)
    # "Оборудование" (платформа Этап 1) — человекочитаемый тип объекта
    # (скважина/насос/УЭЦН/коммутатор и т.п.), детальнее общего kind.
    # Не заменяет kind (тот остаётся техническим для резолвера/корреляции).
    install_date: Mapped[date | None] = mapped_column(Date, nullable=True)
    spec_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    # Свободные характеристики карточки оборудования (JSON-строка) —
    # намеренно не типизировано жёстко: у скважины и у коммутатора разный
    # набор полей, схема на уровне БД не должна знать про каждый тип.


class CmdbAlias(Base):
    """Раздел 4.3, шаг 1 — словарь подтверждённых алиасов."""
    __tablename__ = "cmdb_aliases"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    site: Mapped[str] = mapped_column(String(64), index=True)
    raw_name: Mapped[str] = mapped_column(String(256), index=True)
    object_id: Mapped[str] = mapped_column(ForeignKey("cmdb_objects.id"))

    __table_args__ = (UniqueConstraint("site", "raw_name", name="uq_alias_site_name"),)


class CmdbService(Base):
    __tablename__ = "cmdb_services"

    id: Mapped[str] = mapped_column(String(32), primary_key=True)
    name: Mapped[str] = mapped_column(String(256))
    criticality: Mapped[str] = mapped_column(String(32))


class CmdbServiceObject(Base):
    __tablename__ = "cmdb_service_objects"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    service_id: Mapped[str] = mapped_column(ForeignKey("cmdb_services.id"))
    object_id: Mapped[str] = mapped_column(ForeignKey("cmdb_objects.id"))


class CmdbOwnership(Base):
    """Раздел 4.2 — у объекта и у сервиса может быть несколько владельцев,
    либо ни одного (нормальное состояние данных, не ошибка)."""
    __tablename__ = "cmdb_ownership"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    object_id: Mapped[str | None] = mapped_column(ForeignKey("cmdb_objects.id"), nullable=True)
    service_id: Mapped[str | None] = mapped_column(ForeignKey("cmdb_services.id"), nullable=True)
    subsidiary: Mapped[str] = mapped_column(String(64))


# --- Подписки (раздел 8, M5) -------------------------------------------------
# Раньше все уведомления уходили одному захардкоженному тестовому чату
# (TRUECONF_TEST_RECIPIENT). Теперь адресат вычисляется: у Problem есть
# владелец(ы) через CmdbOwnership (объект и/или сервис), а подписчик сам
# решает, на какой срез этого владения хочет получать уведомления —
# личный кабинет self-service, не правка конфигурации системы.

class Subscriber(Base):
    """Заводится ботом (не веб-страницей!) в ответ на команду в личном
    чате — раздел 20 п.11: платформа сама не ведёт учётные записи (LDAP/AD
    ещё не подключён), поэтому личность подтверждается TrueConf-каналом,
    у которого уже есть собственная аутентификация: только тот, кто
    реально написал боту от имени этого логина, получает access_token
    и ссылку на кабинет. Веб-страница личного кабинета без верного
    access_token никого не пускает — ни на чтение, ни на запись."""
    __tablename__ = "subscribers"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    trueconf_username: Mapped[str] = mapped_column(String(128), unique=True)
    display_name: Mapped[str | None] = mapped_column(String(256), nullable=True)
    access_token: Mapped[str] = mapped_column(String(64), unique=True)
    active: Mapped[bool] = mapped_column(Boolean, default=True)
    # Раздел «Безопасность» — автоотключение при увольнении
    # (packages/common/deprovision.py): False, если TrueConf-логин
    # больше не найден в LDAP-каталоге. Подписки не удаляются (аудит),
    # просто resolve_recipients их больше не видит.
    created_at: Mapped[datetime] = mapped_column(DateTime)
    full_name: Mapped[str | None] = mapped_column(String(256), nullable=True)
    phone: Mapped[str | None] = mapped_column(String(32), nullable=True)
    email: Mapped[str | None] = mapped_column(String(256), nullable=True)
    position: Mapped[str | None] = mapped_column(String(128), nullable=True)
    # Раздел «Сотрудники» (платформа Этап 1) — карточка сотрудника поверх
    # уже существующей подписочной идентичности; все поля nullable и
    # заполняются постепенно, не через bot-token flow (это профильные
    # данные, не механизм авторизации).


class SourceInstance(Base):
    """Паспорт источника (раздел 4.3/11.2): instance -> {system, site} —
    площадка не извлекается из текста алерта, только из регистрации
    инстанса. Раньше жил только в connectors/sources.yaml (правка = правка
    репозитория и редеплой); теперь администратор регистрирует новый
    инстанс через консоль (/sources) без изменения кода — кейс прямо
    требует "подключение новых источников без изменения ядра системы".
    connectors/sources.yaml остаётся демо-затравкой: при пустой таблице
    копируется в БД один раз (packages/common/sources.py), дальше
    источник истины — БД."""
    __tablename__ = "source_instances"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    instance: Mapped[str] = mapped_column(String(128), unique=True)
    system: Mapped[str] = mapped_column(String(64))   # совпадает с source_system коннектора
    site: Mapped[str] = mapped_column(String(64))
    created_at: Mapped[datetime] = mapped_column(DateTime)


class Subscription(Base):
    """Фильтр адресации: NULL по оси = "не сужаю по ней". Подписка совсем
    без фильтров (все поля NULL) — это роль "вижу всё" (дежурная смена
    НОЦ из описания кейса), а не ошибка настройки."""
    __tablename__ = "subscriptions"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    subscriber_id: Mapped[int] = mapped_column(ForeignKey("subscribers.id"), index=True)
    subsidiary: Mapped[str | None] = mapped_column(String(64), nullable=True)
    service_id: Mapped[str | None] = mapped_column(ForeignKey("cmdb_services.id"), nullable=True)
    priority_threshold: Mapped[str | None] = mapped_column(String(8), nullable=True)
    # P0..P3 — присылать проблемы с приоритетом НЕ НИЖЕ порога (P0 самый
    # критичный); NULL — без порога, любой приоритет.
    created_at: Mapped[datetime] = mapped_column(DateTime)


class AuditLog(Base):
    """Раздел «Безопасность» — «предусмотрен мониторинг и аудит действий
    пользователей и администраторов». Пишется в ту же БД, что и всё
    остальное — не теряется при перезапуске контейнеров (в отличие от
    `docker logs`), но и не претендует на неизменяемость/WORM уровня
    Signal (раздел И2) — это эксплуатационный журнал, не источник истины
    о событиях мониторинга."""
    __tablename__ = "audit_log"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    actor: Mapped[str] = mapped_column(String(128))
    action: Mapped[str] = mapped_column(String(64), index=True)
    target: Mapped[str | None] = mapped_column(String(256), nullable=True)
    detail: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, index=True)


class ChangeEvent(Base):
    """История изменений (0011_change_events.sql) — структурированный
    before/after снимок поверх AuditLog, читается картой объекта/
    сотрудника и relay'ится Go-процессом changelog-worker в Kafka/
    ClickHouse для low-code поиска. Python-сторона только пишет
    Postgres — Kafka-клиента здесь нет и не будет (CLAUDE.md: новая
    runtime-функциональность в основном тракте — только на Go)."""
    __tablename__ = "change_events"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    occurred_at: Mapped[datetime] = mapped_column(DateTime, index=True)
    actor: Mapped[str] = mapped_column(String(128))
    actor_role: Mapped[str] = mapped_column(String(32))
    action: Mapped[str] = mapped_column(String(64))
    resource_type: Mapped[str] = mapped_column(String(64), index=True)
    resource_id: Mapped[str] = mapped_column(String(256))
    before_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    after_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    result: Mapped[str] = mapped_column(String(16), default="success")
    detail: Mapped[str | None] = mapped_column(Text, nullable=True)


# --- Платформа, Этап 1: доступность, SLA, сценарии --------------------------

class EmployeeAvailability(Base):
    """Текущий/запланированный статус доступности сотрудника. Источник
    данных явно НЕ определён (пользователь сам отметил это как открытый
    вопрос к менторам — интеграция с HR/TrueConf не выдумана) — на этом
    этапе status выставляется вручную через API/UI, source='manual'
    фиксирует это честно, а не маскирует под автоматическую интеграцию."""
    __tablename__ = "employee_availability"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    subscriber_id: Mapped[int] = mapped_column(ForeignKey("subscribers.id"), index=True)
    status: Mapped[str] = mapped_column(String(32))
    # available | vacation | sick_leave | shift | on_call | unavailable
    valid_from: Mapped[datetime] = mapped_column(DateTime)
    valid_until: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    source: Mapped[str] = mapped_column(String(32), default="manual")
    note: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime)


class SLARule(Base):
    """Раздел «SLA» — целевые нормативы реакции/устранения. Нарушения
    считаются на лету (сравнение Problem.opened_at/resolved_at с
    подходящим правилом) — отдельной таблицы breach нет, чтобы не заводить
    вторую версию истины поверх уже существующих временных меток Problem."""
    __tablename__ = "sla_rules"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(256))
    priority: Mapped[str] = mapped_column(String(8))  # P0-P3
    subsidiary: Mapped[str | None] = mapped_column(String(64), nullable=True)
    service_id: Mapped[str | None] = mapped_column(ForeignKey("cmdb_services.id"), nullable=True)
    response_minutes: Mapped[int] = mapped_column(Integer)
    resolution_minutes: Mapped[int] = mapped_column(Integer)
    created_at: Mapped[datetime] = mapped_column(DateTime)


class Scenario(Base):
    """Раздел «Сценарии» — визуальный no-code конструктор (ReactFlow).
    graph_json — узлы/рёбра как их сохранил редактор. Этап 3:
    packages/scenarios/engine.py на ИСПОЛНЕНИЕ приводит граф к
    ветвящейся структуре (узлы-развилки ack_check/subscription_check с
    рёбрами "да"/"нет", остальные узлы — не более одного исходящего
    ребра) — полный интерпретатор с произвольными циклами по-прежнему не
    строится, только ветвление (см. план платформы, Этап 3).
    status='active' — единственное, что реально участвует в доставке;
    'draft' сохраняется, но не исполняется."""
    __tablename__ = "scenarios"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(256))
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    graph_json: Mapped[str] = mapped_column(Text)  # ReactFlow nodes+edges
    status: Mapped[str] = mapped_column(String(16), default="draft")  # draft | active
    created_by: Mapped[str | None] = mapped_column(String(128), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime)
    updated_at: Mapped[datetime] = mapped_column(DateTime)


class ScenarioRun(Base):
    """Раздел «Сценарии», Этап 3 — состояние исполнения одного активного
    сценария на одной проблеме: в каком узле ветвящегося графа мы сейчас
    и когда в него вошли (нужно для шагов «Подождать»). `current_node_id`
    — id узла ReactFlow (Этап 2 хранил линейный индекс — с ветвлением он
    больше не имеет смысла). Один прогон на пару (сценарий, проблема) —
    повторный проход конвейера не плодит дублей."""
    __tablename__ = "scenario_runs"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    scenario_id: Mapped[int] = mapped_column(ForeignKey("scenarios.id"), index=True)
    problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"), index=True)
    current_node_id: Mapped[str] = mapped_column(String(64))
    status: Mapped[str] = mapped_column(String(16), default="running")  # running | done
    step_entered_at: Mapped[datetime] = mapped_column(DateTime)
    created_at: Mapped[datetime] = mapped_column(DateTime)
    notified_count: Mapped[int] = mapped_column(Integer, default=0)
    # Сколько уведомлений уже отправлено этим прогоном — с ветвлением
    # позиция узла в графе больше не определяет однозначно "это первое
    # уведомление или эскалация" (одна и та же глубина достижима разными
    # путями), поэтому считаем явно, а не выводим из current_node_id.

    __table_args__ = (UniqueConstraint("scenario_id", "problem_id", name="uq_scenario_run_scenario_problem"),)


class SlaBreachNotice(Base):
    """Раздел «SLA», Этап 2 — «уже напомнили по этой проблеме» (одно
    напоминание за жизненный цикл проблемы, не спамим на каждый тик)."""
    __tablename__ = "sla_breach_notices"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    problem_id: Mapped[int] = mapped_column(ForeignKey("problems.id"), index=True)
    sla_rule_id: Mapped[int] = mapped_column(ForeignKey("sla_rules.id"))
    created_at: Mapped[datetime] = mapped_column(DateTime)

    __table_args__ = (UniqueConstraint("problem_id", name="uq_sla_breach_notice_problem"),)
