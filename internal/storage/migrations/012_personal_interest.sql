ALTER TABLE vocabulary_items
    ADD COLUMN personal_interest TEXT NOT NULL DEFAULT 'normal'
    CHECK (personal_interest IN ('low', 'normal', 'high'));
