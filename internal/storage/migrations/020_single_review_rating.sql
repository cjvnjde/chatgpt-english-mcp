-- The migration hook fingerprints original requests before replacing historical
-- submitted grades with the grades used to schedule them, then drops the old column.
ALTER TABLE review_attempts ADD COLUMN request_hash TEXT NOT NULL DEFAULT '';
