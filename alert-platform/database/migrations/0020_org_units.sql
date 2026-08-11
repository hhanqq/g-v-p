-- Раздел «Сотрудники» доп. ТЗ: дерево организации вместо плоского списка
-- карточек. Глубина иерархии произвольная (организация→подразделение→
-- отдел→группа→сотрудник — не фиксированные 4 уровня) — задаётся только
-- самоссылающимся parent_id, без CHECK на глубину или набор уровней.
-- kind — свободная текстовая метка для отображения (например «филиал»,
-- «отдел»), не структурное ограничение: значение не проверяется.
CREATE TABLE IF NOT EXISTS org_units (
    id SERIAL PRIMARY KEY,
    parent_id INTEGER REFERENCES org_units(id),
    name VARCHAR(256) NOT NULL,
    kind VARCHAR(32) NOT NULL DEFAULT 'unit',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_org_units_parent_id ON org_units(parent_id);

ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS org_unit_id INTEGER REFERENCES org_units(id);
CREATE INDEX IF NOT EXISTS ix_subscribers_org_unit_id ON subscribers(org_unit_id);
