ALTER TABLE vocabulary_items
    ADD COLUMN usefulness_hint TEXT
    CHECK (usefulness_hint IS NULL OR usefulness_hint IN ('low', 'normal', 'high'));

UPDATE vocabulary_items SET usefulness_hint = usefulness;

CREATE TABLE usefulness_inference_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision TEXT NOT NULL CHECK (typeof(revision) = 'text' AND length(revision) > 0)
);
