-- Раздел «Покрытие»: минимальное число одновременно доступных
-- дежурных для группы, отвечающей за критичный объект/тип
-- оборудования/площадку. object_id/equipment_type/site — лейбл для
-- отображения в UI ("зачем эта политика"), не сужающий фильтр — сама
-- проверка покрытия всегда идёт по всем участникам group_id, тот же
-- принцип, что у group_equipment_scope (0007_groups.sql).
CREATE TABLE IF NOT EXISTS coverage_policies (
    id SERIAL PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    group_id INTEGER NOT NULL REFERENCES groups(id),
    min_available INTEGER NOT NULL CHECK (min_available >= 1),
    object_id VARCHAR(256) REFERENCES cmdb_objects(id),
    equipment_type VARCHAR(64),
    site VARCHAR(64),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by VARCHAR(128),
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_coverage_policies_group_id ON coverage_policies(group_id);
