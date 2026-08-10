-- Часть I (переработка «Оборудования»): equipment_type сегодня NULL для
-- всех объектов, сгенерированных demo-датагеном (kind — единственная
-- реально заполненная категориальная колонка: server/controller/switch).
-- Backfill даёт группировке по категориям и group_equipment_scope
-- (0007_groups.sql, ось equipment_type) реальные значения без новой
-- таблицы-иерархии — site (4 значения, 1:1 с филиалами кейса) + этот
-- equipment_type (3 значения) уже дают дерево филиал→категория→объект.
UPDATE cmdb_objects SET equipment_type = CASE kind
    WHEN 'controller' THEN 'plc'
    WHEN 'server' THEN 'server'
    WHEN 'switch' THEN 'network'
    ELSE equipment_type
END WHERE equipment_type IS NULL;

CREATE INDEX IF NOT EXISTS ix_cmdb_objects_site_type ON cmdb_objects(site, equipment_type);

-- Часть VIII: свободный список компетенций для карточки сотрудника
-- ("PLC, SCADA, АСУ ТП") — описательное поле по тому же принципу, что
-- уже есть position; ответственность/зона остаётся структурной
-- (group_equipment_scope), не дублируется сюда текстом.
ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS competencies VARCHAR(256);

-- Часть VII: Email как второй канал. channel/contract_version на
-- delivery_outbox уже существуют (0001_delivery_outbox.sql) — не
-- заводим DeliveryCommand v2 как отдельную схему, расширяем те же
-- строки двумя nullable-полями, которые нужны только email (subject,
-- HTML-версия письма); TrueConf-строки их не заполняют, contract
-- назад совместим.
ALTER TABLE delivery_outbox ADD COLUMN IF NOT EXISTS subject VARCHAR(512);
ALTER TABLE delivery_outbox ADD COLUMN IF NOT EXISTS body_html TEXT;

-- Предпочтение канала на сотрудника. По умолчанию TrueConf включён
-- (сохраняет сегодняшнее поведение), email выключен явно — включается
-- осознанно, когда у сотрудника реально есть рабочий email и он должен
-- получать туда уведомления (демо-сид Части VIII включит явно).
ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS trueconf_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS email_enabled BOOLEAN NOT NULL DEFAULT FALSE;
