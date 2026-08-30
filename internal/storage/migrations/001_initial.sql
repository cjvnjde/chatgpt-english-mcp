CREATE TABLE dictionary_snapshots (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    normalized_term TEXT NOT NULL,
    status INTEGER NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    parser_version INTEGER NOT NULL CHECK (parser_version > 0),
    dataset_version TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL CHECK (json_valid(data_json)),
    fetched_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE UNIQUE INDEX dictionary_snapshots_one_active
    ON dictionary_snapshots(provider, normalized_term, dataset_version, parser_version)
    WHERE active = 1;

CREATE INDEX dictionary_snapshots_term
    ON dictionary_snapshots(provider, normalized_term, fetched_at DESC);

CREATE TABLE vocabulary_items (
    id TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    term TEXT NOT NULL,
    normalized_term TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_key, normalized_term)
);

CREATE INDEX vocabulary_items_recent
    ON vocabulary_items(owner_key, updated_at DESC, id);

CREATE INDEX vocabulary_items_alphabetical
    ON vocabulary_items(owner_key, normalized_term, id);

CREATE TABLE explanations (
    id TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    term TEXT NOT NULL,
    normalized_term TEXT NOT NULL,
    context TEXT NOT NULL,
    normalized_context TEXT NOT NULL,
    lookup_id TEXT NOT NULL REFERENCES dictionary_snapshots(id) ON DELETE RESTRICT,
    selected_entry_index INTEGER,
    selected_definition_index INTEGER,
    learner_json TEXT NOT NULL CHECK (json_valid(learner_json)),
    cefr_json TEXT CHECK (cefr_json IS NULL OR json_valid(cefr_json)),
    cefr_level TEXT CHECK (cefr_level IS NULL OR cefr_level IN ('A1', 'A2', 'B1', 'B2', 'C1', 'C2')),
    lexical_relations_json TEXT CHECK (lexical_relations_json IS NULL OR json_valid(lexical_relations_json)),
    generator_name TEXT NOT NULL,
    generator_model TEXT NOT NULL DEFAULT '',
    generator_version TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        (selected_entry_index IS NULL AND selected_definition_index IS NULL)
        OR (selected_entry_index IS NOT NULL AND selected_definition_index IS NOT NULL)
    ),
    UNIQUE(
        owner_key,
        normalized_term,
        normalized_context,
        lookup_id,
        generator_name,
        generator_version
    )
);

CREATE INDEX explanations_natural_key
    ON explanations(
        owner_key,
        normalized_term,
        normalized_context,
        generator_name,
        generator_version,
        updated_at DESC,
        id
    );

CREATE INDEX explanations_recent
    ON explanations(owner_key, updated_at DESC, id);

CREATE INDEX explanations_term
    ON explanations(owner_key, normalized_term, updated_at DESC, id);
