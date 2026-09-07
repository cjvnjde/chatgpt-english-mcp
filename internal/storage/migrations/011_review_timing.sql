ALTER TABLE review_attempts ADD COLUMN effective_rating TEXT
    CHECK (effective_rating IS NULL OR effective_rating IN ('again', 'hard', 'good', 'easy'));

CREATE INDEX learning_presentations_review_token
    ON learning_presentations(owner_key, review_token, learning_card_id);
