ALTER TABLE review_attempts ADD COLUMN last_review_at_before TEXT;

DROP TRIGGER review_attempts_immutable_update;

-- SQLite includes rowid as the trailing key of this index.
CREATE INDEX review_attempts_owner_insertion ON review_attempts(owner_key);

-- Insertion order is authoritative even when the wall clock goes backwards.
-- Only a predecessor with the expected repetition count can restore the clock.
UPDATE review_attempts AS review
SET last_review_at_before = (
    SELECT CASE WHEN previous.repetitions_after = review.repetitions_before
        THEN previous.reviewed_at END
    FROM review_attempts previous
    WHERE previous.owner_key = review.owner_key
      AND previous.learning_card_id = review.learning_card_id
      AND previous.rowid < review.rowid
    ORDER BY previous.rowid DESC LIMIT 1
)
WHERE repetitions_before > 0;

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
BEGIN
    SELECT RAISE(ABORT, 'only the latest review grade, comment, and resulting schedule may be corrected');
END;
