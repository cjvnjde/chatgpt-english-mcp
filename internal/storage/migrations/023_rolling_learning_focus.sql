-- Preserve the size of existing selections, including manually chosen pools.
ALTER TABLE learning_focus_batches ADD COLUMN target_size INTEGER NOT NULL DEFAULT 10
    CHECK (target_size BETWEEN 1 AND 100);

UPDATE learning_focus_batches SET target_size = COALESCE(
    NULLIF((SELECT count(*) FROM learning_focus_items f
        WHERE f.owner_key = learning_focus_batches.owner_key), 0),
    (SELECT json_extract(values_json, '$.focusBatchSize') FROM algorithm_settings s
        WHERE s.owner_key = learning_focus_batches.owner_key),
    10);
