PRAGMA defer_foreign_keys = ON;

CREATE TEMP TABLE learning_cards_before_vocabulary_rebuild AS
SELECT * FROM learning_cards;

CREATE TABLE vocabulary_items_new (
    id TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    term TEXT NOT NULL,
    normalized_term TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    lookup_id TEXT REFERENCES dictionary_snapshots(id) ON DELETE RESTRICT,
    custom_description TEXT NOT NULL DEFAULT '',
    learning_status TEXT NOT NULL DEFAULT 'new'
        CHECK (learning_status IN ('new', 'learning', 'learned', 'archived')),
    description_source_json TEXT
        CHECK (description_source_json IS NULL OR json_valid(description_source_json)),
    notes_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(notes_json) AND json_type(notes_json) = 'array'),
    examples_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(examples_json) AND json_type(examples_json) = 'array'),
    tags_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(tags_json) AND json_type(tags_json) = 'array'),
    sense_key TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    selected_entry_index INTEGER,
    selected_definition_index INTEGER,
    selected_definition_json TEXT
        CHECK (selected_definition_json IS NULL OR json_valid(selected_definition_json)),
    CHECK (
        (selected_entry_index IS NULL AND selected_definition_index IS NULL AND selected_definition_json IS NULL)
        OR (selected_entry_index IS NOT NULL AND selected_definition_index IS NOT NULL AND selected_definition_json IS NOT NULL)
    ),
    UNIQUE(owner_key, normalized_term, sense_key)
);

INSERT INTO vocabulary_items_new(
    id, owner_key, term, normalized_term, created_at, updated_at, lookup_id,
    custom_description, learning_status, description_source_json, notes_json,
    examples_json, tags_json, sense_key
)
SELECT
    id, owner_key, term, normalized_term, created_at, updated_at, lookup_id,
    custom_description, learning_status, description_source_json, notes_json,
    examples_json, tags_json, 'legacy'
FROM vocabulary_items;

DROP TABLE vocabulary_items;
ALTER TABLE vocabulary_items_new RENAME TO vocabulary_items;

INSERT INTO learning_cards
SELECT * FROM learning_cards_before_vocabulary_rebuild;
DROP TABLE learning_cards_before_vocabulary_rebuild;

CREATE INDEX vocabulary_items_recent ON vocabulary_items(owner_key, updated_at DESC, id);
CREATE INDEX vocabulary_items_alphabetical ON vocabulary_items(owner_key, normalized_term, id);
CREATE INDEX vocabulary_items_lookup ON vocabulary_items(lookup_id);
CREATE INDEX vocabulary_items_status ON vocabulary_items(owner_key, learning_status, updated_at DESC, id);

CREATE TRIGGER vocabulary_items_create_production_card
AFTER INSERT ON vocabulary_items
WHEN NEW.learning_status <> 'archived'
BEGIN
    INSERT INTO learning_cards(
        id, vocabulary_item_id, exercise_mode, due_at, created_at, updated_at
    ) VALUES (
        NEW.id || ':production', NEW.id, 'production', NEW.created_at, NEW.created_at, NEW.updated_at
    );
END;

CREATE TRIGGER vocabulary_items_reactivate_production_card
AFTER UPDATE OF learning_status ON vocabulary_items
WHEN OLD.learning_status = 'archived' AND NEW.learning_status <> 'archived'
BEGIN
    INSERT OR IGNORE INTO learning_cards(
        id, vocabulary_item_id, exercise_mode, due_at, created_at, updated_at
    ) VALUES (
        NEW.id || ':production', NEW.id, 'production', NEW.updated_at, NEW.updated_at, NEW.updated_at
    );
END;
