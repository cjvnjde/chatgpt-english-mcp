ALTER TABLE learning_cards ADD COLUMN mastery_streak INTEGER NOT NULL DEFAULT 0 CHECK (mastery_streak >= 0);
ALTER TABLE learning_cards ADD COLUMN last_mastery_at TEXT;

-- Historical mastery was not measured. Do not infer it from grades or change
-- manually assigned vocabulary statuses during migration.
ALTER TABLE review_attempts ADD COLUMN status_before TEXT CHECK (status_before IN ('new', 'learning', 'learned', 'archived'));
ALTER TABLE review_attempts ADD COLUMN status_after TEXT NOT NULL DEFAULT 'learning' CHECK (status_after IN ('new', 'learning', 'learned', 'archived'));
ALTER TABLE review_attempts ADD COLUMN reinforcement_count_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE review_attempts ADD COLUMN mastery_streak_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE review_attempts ADD COLUMN last_mastery_at_before TEXT;
ALTER TABLE review_attempts ADD COLUMN mastery_streak_after INTEGER NOT NULL DEFAULT 0;
ALTER TABLE review_attempts ADD COLUMN last_mastery_at_after TEXT;

DROP TRIGGER review_attempts_guard_update;

-- NULL status_before explicitly marks legacy attempts: corrections retain the
-- current manual status because the original status is unknowable. Freeze the
-- available status for retries, even after vocabulary deletion.
UPDATE review_attempts SET status_after = COALESCE(
    (SELECT learning_status FROM vocabulary_items WHERE id = vocabulary_item_id), 'learning'),
    reinforcement_count_before = COALESCE(
    (SELECT review_count FROM reinforcement_practice WHERE vocabulary_item_id = review_attempts.vocabulary_item_id), 0);

CREATE TRIGGER review_attempts_guard_update
BEFORE UPDATE ON review_attempts
WHEN OLD.rowid <> (SELECT max(rowid) FROM review_attempts WHERE owner_key = OLD.owner_key)
  OR NEW.rowid IS NOT OLD.rowid
  OR NEW.id IS NOT OLD.id
  OR NEW.owner_key IS NOT OLD.owner_key
  OR NEW.submission_id IS NOT OLD.submission_id
  OR NEW.vocabulary_item_id IS NOT OLD.vocabulary_item_id
  OR NEW.learning_card_id IS NOT OLD.learning_card_id
  OR NEW.exercise_mode IS NOT OLD.exercise_mode
  OR NEW.reviewed_at IS NOT OLD.reviewed_at
  OR NEW.due_before IS NOT OLD.due_before
  OR NEW.stability_before IS NOT OLD.stability_before
  OR NEW.difficulty_before IS NOT OLD.difficulty_before
  OR NEW.retrievability_before IS NOT OLD.retrievability_before
  OR NEW.scheduled_days_before IS NOT OLD.scheduled_days_before
  OR NEW.repetitions_before IS NOT OLD.repetitions_before
  OR NEW.lapses_before IS NOT OLD.lapses_before
  OR NEW.fsrs_state_before IS NOT OLD.fsrs_state_before
  OR NEW.remaining_steps_before IS NOT OLD.remaining_steps_before
  OR NEW.consecutive_failures_before IS NOT OLD.consecutive_failures_before
  OR NEW.last_review_at_before IS NOT OLD.last_review_at_before
  OR NEW.status_before IS NOT OLD.status_before
  OR NEW.reinforcement_count_before IS NOT OLD.reinforcement_count_before
  OR NEW.mastery_streak_before IS NOT OLD.mastery_streak_before
  OR NEW.last_mastery_at_before IS NOT OLD.last_mastery_at_before
BEGIN
    SELECT RAISE(ABORT, 'only the latest review grade, comment, and resulting state may be corrected');
END;

-- Reinforcement was learned-only and never changed status before this migration.
ALTER TABLE reinforcement_attempts ADD COLUMN status_after TEXT NOT NULL DEFAULT 'learned' CHECK (status_after IN ('learning', 'learned'));
