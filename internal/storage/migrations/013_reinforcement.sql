CREATE TABLE reinforcement_practice (
    vocabulary_item_id TEXT PRIMARY KEY REFERENCES vocabulary_items(id) ON DELETE CASCADE,
    review_count INTEGER NOT NULL DEFAULT 0 CHECK (review_count >= 0),
    difficulty REAL NOT NULL DEFAULT 0 CHECK (difficulty BETWEEN 0 AND 4),
    last_rating TEXT CHECK (last_rating IS NULL OR last_rating IN ('again', 'hard', 'good', 'easy')),
    last_reviewed_at TEXT,
    last_shown_at TEXT NOT NULL
);

-- Presentations and attempts retain their evidence when vocabulary is deleted.
CREATE TABLE reinforcement_presentations (
    review_token TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    vocabulary_item_id TEXT NOT NULL,
    shown_at TEXT NOT NULL
);

CREATE TABLE reinforcement_attempts (
    id TEXT PRIMARY KEY,
    review_token TEXT NOT NULL UNIQUE REFERENCES reinforcement_presentations(review_token),
    owner_key TEXT NOT NULL,
    vocabulary_item_id TEXT NOT NULL,
    rating TEXT NOT NULL CHECK (rating IN ('again', 'hard', 'good', 'easy')),
    comment TEXT NOT NULL,
    reviewed_at TEXT NOT NULL,
    review_count_after INTEGER NOT NULL CHECK (review_count_after > 0),
    difficulty_after REAL NOT NULL CHECK (difficulty_after BETWEEN 0 AND 4)
);

CREATE INDEX reinforcement_attempts_item_history
    ON reinforcement_attempts(owner_key, vocabulary_item_id, reviewed_at, id);

CREATE TRIGGER reinforcement_presentations_immutable_update
BEFORE UPDATE ON reinforcement_presentations
BEGIN
    SELECT RAISE(ABORT, 'reinforcement presentations are immutable');
END;

CREATE TRIGGER reinforcement_presentations_immutable_delete
BEFORE DELETE ON reinforcement_presentations
BEGIN
    SELECT RAISE(ABORT, 'reinforcement presentations are immutable');
END;

CREATE TRIGGER reinforcement_attempts_immutable_update
BEFORE UPDATE ON reinforcement_attempts
BEGIN
    SELECT RAISE(ABORT, 'reinforcement attempts are immutable');
END;

CREATE TRIGGER reinforcement_attempts_immutable_delete
BEFORE DELETE ON reinforcement_attempts
BEGIN
    SELECT RAISE(ABORT, 'reinforcement attempts are immutable');
END;
