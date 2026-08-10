-- Раздел «Маршрутизация с учётом доступности» — каждая доставка теперь
-- может нести небольшой decision trace (доступен ли получатель на
-- момент отправки, чем это подкреплено, и делегирован ли он кем-то
-- другим). Опционально: NULL для доставок, не прошедших через
-- resolveRoutingDecisions (SLA/сценарные пути, где решение фиксируется
-- в scenario_run_steps.recipients_json, не здесь).
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS routing_trace_json TEXT;
