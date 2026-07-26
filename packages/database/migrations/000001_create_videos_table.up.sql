CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE video_status AS ENUM (
    'uploading',
    'processing',
    'ready',
    'failed'
);

CREATE TABLE videos (
    id              UUID NOT NULL DEFAULT gen_random_uuid(),
    title           TEXT NOT NULL,
    status          video_status NOT NULL DEFAULT 'uploading',
    duration        DOUBLE PRECISION,
    width           INTEGER,
    height          INTEGER,
    size_bytes      BIGINT,
    master_playlist TEXT,
    thumbnail       TEXT,
    source_bucket   TEXT NOT NULL,
    source_key      TEXT NOT NULL,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT videos_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_videos_status ON videos (status);

CREATE INDEX idx_videos_created_at ON videos (created_at DESC);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER videos_set_updated_at
    BEFORE UPDATE ON videos
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
