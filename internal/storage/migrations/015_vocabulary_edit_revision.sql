ALTER TABLE vocabulary_items
    ADD COLUMN edit_revision INTEGER NOT NULL DEFAULT 1 CHECK (edit_revision > 0);

-- Only editable metadata invalidates an admin draft, not timestamps or review history.
CREATE TRIGGER vocabulary_items_edit_revision
AFTER UPDATE OF learning_status, usefulness, usefulness_hint, personal_interest,
    tags_json, custom_description, description_source_json, notes_json, examples_json
ON vocabulary_items
WHEN OLD.learning_status IS NOT NEW.learning_status
    OR OLD.usefulness IS NOT NEW.usefulness
    OR OLD.usefulness_hint IS NOT NEW.usefulness_hint
    OR OLD.personal_interest IS NOT NEW.personal_interest
    OR OLD.tags_json IS NOT NEW.tags_json
    OR OLD.custom_description IS NOT NEW.custom_description
    OR OLD.description_source_json IS NOT NEW.description_source_json
    OR OLD.notes_json IS NOT NEW.notes_json
    OR OLD.examples_json IS NOT NEW.examples_json
BEGIN
    UPDATE vocabulary_items SET edit_revision = OLD.edit_revision + 1 WHERE id = NEW.id;
END;
