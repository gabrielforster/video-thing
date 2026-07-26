-- name: CreateVideo :one
INSERT INTO videos (id, title, source_bucket, source_key)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetVideo :one
SELECT * FROM videos WHERE id = $1;

-- name: GetVideoForUpdate :one
SELECT * FROM videos WHERE id = $1 FOR UPDATE;

-- name: MarkProcessing :one
UPDATE videos
SET status = 'processing'
WHERE id = $1
RETURNING *;

-- name: MarkProcessingFromUploading :one
UPDATE videos
SET status = 'processing'
WHERE id = $1 AND status = 'uploading'
RETURNING *;

-- name: MarkReady :one
UPDATE videos
SET status = 'ready',
    duration = $2,
    width = $3,
    height = $4,
    size_bytes = $5,
    master_playlist = $6,
    thumbnail = $7,
    error_message = NULL
WHERE id = $1
RETURNING *;

-- name: MarkFailed :one
UPDATE videos
SET status = 'failed', error_message = $2
WHERE id = $1
RETURNING *;
