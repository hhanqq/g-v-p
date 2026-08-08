DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'ck_delivery_outbox_target'
    ) THEN
        ALTER TABLE delivery_outbox
            ADD CONSTRAINT ck_delivery_outbox_target
            CHECK (recipient IS NOT NULL OR provider_chat_id IS NOT NULL);
    END IF;
END
$$;
