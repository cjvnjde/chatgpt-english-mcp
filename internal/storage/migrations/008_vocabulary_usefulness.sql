ALTER TABLE vocabulary_items
    ADD COLUMN usefulness TEXT NOT NULL DEFAULT 'normal'
    CHECK (usefulness IN ('low', 'normal', 'high'));
