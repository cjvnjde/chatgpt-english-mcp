CREATE TABLE learning_presentations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_key TEXT NOT NULL,
    vocabulary_item_id TEXT NOT NULL,
    learning_card_id TEXT NOT NULL,
    exercise_mode TEXT NOT NULL,
    review_token TEXT NOT NULL,
    shown_at TEXT NOT NULL,
    due_at TEXT NOT NULL,
    selection_kind TEXT NOT NULL CHECK (selection_kind IN ('new', 'due', 'early'))
);

CREATE INDEX learning_presentations_recent
    ON learning_presentations(owner_key, exercise_mode, id DESC);
CREATE INDEX learning_presentations_card_history
    ON learning_presentations(owner_key, learning_card_id, id DESC);

CREATE TRIGGER learning_presentations_immutable_update
BEFORE UPDATE ON learning_presentations
BEGIN
    SELECT RAISE(ABORT, 'learning presentations are immutable');
END;

CREATE TRIGGER learning_presentations_immutable_delete
BEFORE DELETE ON learning_presentations
BEGIN
    SELECT RAISE(ABORT, 'learning presentations are immutable');
END;
