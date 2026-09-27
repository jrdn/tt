-- Insertion order, used to break created_at ties (timestamps have
-- one-second resolution). SQLite uses rowid for the same purpose.
ALTER TABLE tasks ADD COLUMN seq BIGSERIAL;
ALTER TABLE comments ADD COLUMN seq BIGSERIAL;
