package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func permanent(format string, args ...any) error {
	return &permanentError{err: fmt.Errorf(format, args...)}
}

type workerStore interface {
	MarkProcessing(ctx context.Context, id uuid.UUID) (db.Video, error)
	MarkReady(ctx context.Context, arg db.MarkReadyParams) (db.Video, error)
	MarkFailed(ctx context.Context, arg db.MarkFailedParams) (db.Video, error)
}

type pipeline struct {
	store           workerStore
	s3              *s3.Client
	processedBucket string
}

func objectKey(videoID uuid.UUID, root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside output root %q", path, root)
	}
	return "processed/" + videoID.String() + "/" + filepath.ToSlash(rel), nil
}

func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/mp2t"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}

func (p *pipeline) process(ctx context.Context, job uploadedObject) error {
	if _, err := p.store.MarkProcessing(ctx, job.VideoID); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}

	work, err := os.MkdirTemp("", "video-"+job.VideoID.String()+"-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(work)

	source := filepath.Join(work, "source")
	if err := p.download(ctx, job, source); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	probe, err := p.probe(ctx, source)
	if err != nil {
		return err
	}

	out := filepath.Join(work, "out")
	if err := os.MkdirAll(filepath.Join(out, renditionDir), 0o755); err != nil {
		return fmt.Errorf("output dir: %w", err)
	}
	if err := run(ctx, "ffmpeg", transcodeArgs(source, out)); err != nil {
		return fmt.Errorf("transcode: %w", err)
	}

	if err := os.WriteFile(filepath.Join(out, "master.m3u8"), []byte(masterPlaylist()), 0o644); err != nil {
		return fmt.Errorf("write master playlist: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(out, "thumbnails"), 0o755); err != nil {
		return fmt.Errorf("thumbnail dir: %w", err)
	}
	cover := filepath.Join(out, "thumbnails", "cover.jpg")
	if err := run(ctx, "ffmpeg", coverArgs(source, cover, probe.Duration)); err != nil {
		return fmt.Errorf("cover: %w", err)
	}

	if err := p.uploadTree(ctx, job.VideoID, out); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	size := job.Size
	playlistKey := "processed/" + job.VideoID.String() + "/master.m3u8"
	coverKey := "processed/" + job.VideoID.String() + "/thumbnails/cover.jpg"
	duration, width, height := probe.Duration, probe.Width, probe.Height

	if _, err := p.store.MarkReady(ctx, db.MarkReadyParams{
		ID:             job.VideoID,
		Duration:       &duration,
		Width:          &width,
		Height:         &height,
		SizeBytes:      &size,
		MasterPlaylist: &playlistKey,
		Thumbnail:      &coverKey,
	}); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	return nil
}

func (p *pipeline) download(ctx context.Context, job uploadedObject, dest string) error {
	obj, err := p.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(job.Bucket),
		Key:    aws.String(job.Key),
	})
	if err != nil {
		return err
	}
	defer obj.Body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, obj.Body)
	return err
}

func (p *pipeline) probe(ctx context.Context, source string) (probeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", probeArgs(source)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return probeResult{}, permanent("ffprobe: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	result, err := parseProbe(stdout.Bytes())
	if err != nil {
		return probeResult{}, permanent("%v", err)
	}
	return result, nil
}

func (p *pipeline) uploadTree(ctx context.Context, videoID uuid.UUID, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		key, err := objectKey(videoID, root, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = p.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(p.processedBucket),
			Key:         aws.String(key),
			Body:        f,
			ContentType: aws.String(contentTypeFor(path)),
		})
		return err
	})
}

func run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
