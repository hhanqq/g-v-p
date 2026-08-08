ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS recipient VARCHAR(256);

UPDATE notifications AS notification
SET recipient = COALESCE(outbox.recipient, 'chat:' || notification.chat_id)
FROM delivery_outbox AS outbox
WHERE outbox.notification_id = notification.id
  AND notification.recipient IS NULL;

UPDATE notifications
SET recipient = 'chat:' || chat_id
WHERE recipient IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_notification_problem_type_recipient
    ON notifications(problem_id, type, recipient)
    WHERE recipient IS NOT NULL;
