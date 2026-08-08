ALTER TABLE notifications
    ALTER COLUMN recipient TYPE VARCHAR(256);

ALTER TABLE delivery_outbox
    ALTER COLUMN recipient TYPE VARCHAR(256);
