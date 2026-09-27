CREATE TABLE algorithm_settings (
    owner_key TEXT PRIMARY KEY NOT NULL,
    values_json TEXT NOT NULL CHECK (json_valid(values_json)),
    revision INTEGER NOT NULL CHECK (revision > 0)
);
