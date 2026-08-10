-- Типизированная модель доступности: employee_availability уже была
-- таблицей интервалов (valid_from/valid_until), но единственное поле
-- status было нетипизированной строкой без ограничения значений, и
-- ничто в коде не проверяло valid_until при чтении "текущего статуса".
-- kind — типизированная замена status (тот остаётся для обратной
-- совместимости чтения старых строк, но больше не пишется), с
-- приоритетом при пересечении интервалов (internal/availability).
ALTER TABLE employee_availability ADD COLUMN IF NOT EXISTS kind VARCHAR(32);
UPDATE employee_availability SET kind = CASE
    WHEN status = 'available' THEN 'available'
    WHEN status = 'shift' THEN 'shift'
    WHEN status = 'on_call' THEN 'on_call'
    WHEN status = 'vacation' THEN 'vacation'
    WHEN status = 'sick_leave' THEN 'sick_leave'
    WHEN status = 'unavailable' THEN 'unavailable'
    ELSE 'unavailable'
END WHERE kind IS NULL;
ALTER TABLE employee_availability ALTER COLUMN kind SET NOT NULL;
DO $$ BEGIN
    ALTER TABLE employee_availability ADD CONSTRAINT ck_employee_availability_kind CHECK (
        kind IN ('override_available', 'override_unavailable', 'sick_leave', 'vacation',
                 'delegation', 'shift', 'on_call', 'unavailable', 'available')
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE employee_availability ADD COLUMN IF NOT EXISTS delegate_to_subscriber_id INTEGER REFERENCES subscribers(id);
DO $$ BEGIN
    ALTER TABLE employee_availability ADD CONSTRAINT ck_employee_availability_delegate CHECK (
        (kind = 'delegation') = (delegate_to_subscriber_id IS NOT NULL)
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE employee_availability ALTER COLUMN status DROP NOT NULL;
