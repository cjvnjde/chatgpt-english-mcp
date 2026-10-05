-- Preserve existing selection behavior and settings revisions on upgrade.
UPDATE algorithm_settings SET values_json = json_set(values_json,
    '$.learningMode', 'mixed', '$.focusBatchSize', 10);

CREATE TABLE learning_focus_batches (
    owner_key TEXT PRIMARY KEY NOT NULL,
    id TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE learning_focus_items (
    owner_key TEXT NOT NULL REFERENCES learning_focus_batches(owner_key) ON DELETE CASCADE,
    vocabulary_item_id TEXT NOT NULL REFERENCES vocabulary_items(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (owner_key, vocabulary_item_id),
    UNIQUE (owner_key, position)
);

-- Explicit archival removes membership permanently, even if later unarchived.
CREATE TRIGGER learning_focus_archive AFTER UPDATE OF learning_status ON vocabulary_items
WHEN NEW.learning_status = 'archived'
BEGIN
    DELETE FROM learning_focus_items WHERE vocabulary_item_id = NEW.id;
END;
