DROP INDEX vocabulary_items_recent;
CREATE INDEX vocabulary_items_recent
    ON vocabulary_items(owner_key, rtrim(updated_at, 'Z') DESC, id);

DROP INDEX vocabulary_items_status;
CREATE INDEX vocabulary_items_status
    ON vocabulary_items(owner_key, learning_status, rtrim(updated_at, 'Z') DESC, id);

DROP INDEX review_attempts_item_history;
CREATE INDEX review_attempts_item_history
    ON review_attempts(owner_key, vocabulary_item_id, rtrim(reviewed_at, 'Z'), id);
