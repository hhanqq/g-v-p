-- Идемпотентность команд теперь принадлежит delivery_outbox.idempotency_key.
-- Старый recipient-index не позволял сценарию отправить несколько разных
-- шагов одному сотруднику по одной Problem.
DROP INDEX IF EXISTS uq_notification_problem_type_recipient;
