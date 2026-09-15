CREATE TABLE media_objects (
    id TEXT PRIMARY KEY
        CHECK (length(id) = 64 AND id NOT GLOB '*[^0-9a-f]*'),
    content_type TEXT NOT NULL
        CHECK (content_type IN (
            'audio/mpeg', 'audio/mp4', 'audio/ogg', 'audio/wav',
            'image/avif', 'image/gif', 'image/jpeg', 'image/png', 'image/webp'
        )),
    byte_size INTEGER NOT NULL CHECK (byte_size > 0),
    data BLOB NOT NULL CHECK (length(data) = byte_size),
    source_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE vocabulary_images (
    id TEXT PRIMARY KEY,
    owner_key TEXT NOT NULL,
    vocabulary_item_id TEXT NOT NULL REFERENCES vocabulary_items(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media_objects(id) ON DELETE RESTRICT,
    original_filename TEXT NOT NULL DEFAULT '',
    example TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX vocabulary_images_item
    ON vocabulary_images(owner_key, vocabulary_item_id, created_at, id);

CREATE TRIGGER vocabulary_images_edit_revision_insert
AFTER INSERT ON vocabulary_images
BEGIN
    UPDATE vocabulary_items
    SET updated_at = NEW.created_at,
        edit_revision = edit_revision + 1
    WHERE id = NEW.vocabulary_item_id AND owner_key = NEW.owner_key;
END;

CREATE TRIGGER vocabulary_images_edit_revision_delete
AFTER DELETE ON vocabulary_images
BEGIN
    UPDATE vocabulary_items
    SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
        edit_revision = edit_revision + 1
    WHERE id = OLD.vocabulary_item_id AND owner_key = OLD.owner_key;
END;

CREATE TRIGGER vocabulary_images_media_cleanup
AFTER DELETE ON vocabulary_images
BEGIN
    DELETE FROM media_objects
    WHERE id = OLD.media_id
      AND NOT EXISTS (
          SELECT 1 FROM vocabulary_images WHERE media_id = OLD.media_id
      )
      AND NOT EXISTS (
          SELECT 1
          FROM dictionary_snapshots, json_tree(dictionary_snapshots.data_json)
          WHERE json_tree.key IN ('mediaId', 'thumbnailMediaId')
            AND json_tree.value = OLD.media_id
      );
END;
