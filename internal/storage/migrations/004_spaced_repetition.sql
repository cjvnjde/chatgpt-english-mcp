CREATE TABLE learning_cards (
    id TEXT PRIMARY KEY,
    vocabulary_item_id TEXT NOT NULL REFERENCES vocabulary_items(id) ON DELETE CASCADE,
    exercise_mode TEXT NOT NULL,
    due_at TEXT NOT NULL,
    stability REAL NOT NULL DEFAULT 0 CHECK (stability >= 0),
    difficulty REAL NOT NULL DEFAULT 0 CHECK (difficulty >= 0 AND difficulty <= 10),
    retrievability REAL NOT NULL DEFAULT 0 CHECK (retrievability >= 0 AND retrievability <= 1),
    scheduled_days INTEGER NOT NULL DEFAULT 0 CHECK (scheduled_days >= 0),
    repetitions INTEGER NOT NULL DEFAULT 0 CHECK (repetitions >= 0),
    lapses INTEGER NOT NULL DEFAULT 0 CHECK (lapses >= 0),
    fsrs_state INTEGER NOT NULL DEFAULT 0 CHECK (fsrs_state BETWEEN 0 AND 3),
    last_review_at TEXT,
    remaining_steps INTEGER NOT NULL DEFAULT 0 CHECK (remaining_steps >= 0),
    last_rating TEXT CHECK (last_rating IS NULL OR last_rating IN ('again', 'hard', 'good', 'easy')),
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(vocabulary_item_id, exercise_mode)
);

CREATE INDEX learning_cards_due
    ON learning_cards(exercise_mode, fsrs_state, due_at, vocabulary_item_id);

CREATE TABLE review_attempts (
    id TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    submission_id TEXT NOT NULL,
    vocabulary_item_id TEXT NOT NULL,
    learning_card_id TEXT NOT NULL,
    exercise_mode TEXT NOT NULL,
    rating TEXT NOT NULL CHECK (rating IN ('again', 'hard', 'good', 'easy')),
    reviewed_at TEXT NOT NULL,
    due_before TEXT NOT NULL,
    stability_before REAL NOT NULL CHECK (stability_before >= 0),
    difficulty_before REAL NOT NULL CHECK (difficulty_before >= 0 AND difficulty_before <= 10),
    retrievability_before REAL NOT NULL CHECK (retrievability_before >= 0 AND retrievability_before <= 1),
    scheduled_days_before INTEGER NOT NULL CHECK (scheduled_days_before >= 0),
    repetitions_before INTEGER NOT NULL CHECK (repetitions_before >= 0),
    lapses_before INTEGER NOT NULL CHECK (lapses_before >= 0),
    fsrs_state_before INTEGER NOT NULL CHECK (fsrs_state_before BETWEEN 0 AND 3),
    remaining_steps_before INTEGER NOT NULL CHECK (remaining_steps_before >= 0),
    consecutive_failures_before INTEGER NOT NULL CHECK (consecutive_failures_before >= 0),
    due_after TEXT NOT NULL,
    stability_after REAL NOT NULL CHECK (stability_after >= 0),
    difficulty_after REAL NOT NULL CHECK (difficulty_after >= 0 AND difficulty_after <= 10),
    retrievability_after REAL NOT NULL CHECK (retrievability_after >= 0 AND retrievability_after <= 1),
    scheduled_days_after INTEGER NOT NULL CHECK (scheduled_days_after >= 0),
    repetitions_after INTEGER NOT NULL CHECK (repetitions_after >= 0),
    lapses_after INTEGER NOT NULL CHECK (lapses_after >= 0),
    fsrs_state_after INTEGER NOT NULL CHECK (fsrs_state_after BETWEEN 0 AND 3),
    remaining_steps_after INTEGER NOT NULL CHECK (remaining_steps_after >= 0),
    consecutive_failures_after INTEGER NOT NULL CHECK (consecutive_failures_after >= 0),
    UNIQUE(owner_key, submission_id)
);

CREATE INDEX review_attempts_item_history
    ON review_attempts(owner_key, vocabulary_item_id, reviewed_at, id);

CREATE TRIGGER review_attempts_immutable_update
BEFORE UPDATE ON review_attempts
BEGIN
    SELECT RAISE(ABORT, 'review attempts are immutable');
END;

CREATE TRIGGER review_attempts_immutable_delete
BEFORE DELETE ON review_attempts
BEGIN
    SELECT RAISE(ABORT, 'review attempts are immutable');
END;

INSERT INTO learning_cards(
    id,
    vocabulary_item_id,
    exercise_mode,
    due_at,
    created_at,
    updated_at
)
SELECT
    id || ':production',
    id,
    'production',
    created_at,
    created_at,
    updated_at
FROM vocabulary_items
WHERE learning_status <> 'archived';

CREATE TRIGGER vocabulary_items_create_production_card
AFTER INSERT ON vocabulary_items
WHEN NEW.learning_status <> 'archived'
BEGIN
    INSERT INTO learning_cards(
        id,
        vocabulary_item_id,
        exercise_mode,
        due_at,
        created_at,
        updated_at
    ) VALUES (
        NEW.id || ':production',
        NEW.id,
        'production',
        NEW.created_at,
        NEW.created_at,
        NEW.updated_at
    );
END;

CREATE TRIGGER vocabulary_items_reactivate_production_card
AFTER UPDATE OF learning_status ON vocabulary_items
WHEN OLD.learning_status = 'archived' AND NEW.learning_status <> 'archived'
BEGIN
    INSERT OR IGNORE INTO learning_cards(
        id,
        vocabulary_item_id,
        exercise_mode,
        due_at,
        created_at,
        updated_at
    ) VALUES (
        NEW.id || ':production',
        NEW.id,
        'production',
        NEW.updated_at,
        NEW.updated_at,
        NEW.updated_at
    );
END;
