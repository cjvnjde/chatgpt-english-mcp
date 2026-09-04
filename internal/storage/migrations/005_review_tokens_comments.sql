ALTER TABLE learning_cards
    ADD COLUMN review_token TEXT;

UPDATE learning_cards
SET review_token = lower(hex(randomblob(16)))
WHERE review_token IS NULL;

CREATE UNIQUE INDEX learning_cards_review_token
    ON learning_cards(review_token)
    WHERE review_token IS NOT NULL;

CREATE TRIGGER learning_cards_assign_review_token
AFTER INSERT ON learning_cards
WHEN NEW.review_token IS NULL
BEGIN
    UPDATE learning_cards
    SET review_token = lower(hex(randomblob(16)))
    WHERE id = NEW.id;
END;

ALTER TABLE review_attempts
    ADD COLUMN comment TEXT NOT NULL DEFAULT ''
    CHECK (length(comment) <= 1000);
