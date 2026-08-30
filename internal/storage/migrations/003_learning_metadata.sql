ALTER TABLE vocabulary_items
    ADD COLUMN learning_status TEXT NOT NULL DEFAULT 'new'
    CHECK (learning_status IN ('new', 'learning', 'learned', 'archived'));

ALTER TABLE vocabulary_items
    ADD COLUMN description_source_json TEXT
    CHECK (description_source_json IS NULL OR json_valid(description_source_json));

ALTER TABLE vocabulary_items
    ADD COLUMN notes_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(notes_json) AND json_type(notes_json) = 'array');

ALTER TABLE vocabulary_items
    ADD COLUMN examples_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(examples_json) AND json_type(examples_json) = 'array');

ALTER TABLE vocabulary_items
    ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(tags_json) AND json_type(tags_json) = 'array');

CREATE INDEX vocabulary_items_status
    ON vocabulary_items(owner_key, learning_status, updated_at DESC, id);
