DROP TABLE explanations;

ALTER TABLE vocabulary_items
    ADD COLUMN lookup_id TEXT REFERENCES dictionary_snapshots(id) ON DELETE RESTRICT;

ALTER TABLE vocabulary_items
    ADD COLUMN custom_description TEXT NOT NULL DEFAULT '';

UPDATE vocabulary_items
SET lookup_id = (
    SELECT snapshot.id
    FROM dictionary_snapshots snapshot
    WHERE snapshot.normalized_term = vocabulary_items.normalized_term
      AND snapshot.active = 1
      AND json_array_length(snapshot.data_json, '$.entries') > 0
    ORDER BY snapshot.fetched_at DESC, snapshot.id
    LIMIT 1
);

CREATE INDEX vocabulary_items_lookup
    ON vocabulary_items(lookup_id);
