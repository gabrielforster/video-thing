package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestObjectKeyIsRelativeToTheOutputRoot(t *testing.T) {
	id := uuid.MustParse("3fa85f64-5717-4562-b3fc-2c963f66afa6")

	cases := map[string]string{
		"/tmp/work/out/master.m3u8":          "processed/3fa85f64-5717-4562-b3fc-2c963f66afa6/master.m3u8",
		"/tmp/work/out/720/playlist.m3u8":    "processed/3fa85f64-5717-4562-b3fc-2c963f66afa6/720/playlist.m3u8",
		"/tmp/work/out/720/segment_00000.ts": "processed/3fa85f64-5717-4562-b3fc-2c963f66afa6/720/segment_00000.ts",
		"/tmp/work/out/thumbnails/cover.jpg": "processed/3fa85f64-5717-4562-b3fc-2c963f66afa6/thumbnails/cover.jpg",
	}
	for path, want := range cases {
		got, err := objectKey(id, "/tmp/work/out", path)
		if err != nil {
			t.Fatalf("objectKey(%q): %v", path, err)
		}
		if got != want {
			t.Errorf("objectKey(%q) = %q, want %q", path, got, want)
		}
	}

	if _, err := objectKey(id, "/tmp/work/out", "/etc/passwd"); err == nil {
		t.Fatal("expected an error for a path outside the output root")
	}
}

func TestRecordedKeysAreKeysThatGetUploaded(t *testing.T) {
	id := uuid.MustParse("3fa85f64-5717-4562-b3fc-2c963f66afa6")
	out := newOutputPaths(t.TempDir())

	for _, path := range []string{
		out.playlist,
		out.cover,
		filepath.Join(out.root, renditionDir, "playlist.m3u8"),
		filepath.Join(out.root, renditionDir, "segment_00000.ts"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}

	uploaded := map[string]bool{}
	err := filepath.WalkDir(out.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		key, err := objectKey(id, out.root, path)
		if err != nil {
			return err
		}
		uploaded[key] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the output tree: %v", err)
	}

	playlistKey, coverKey, err := out.keys(id)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	for _, key := range []string{playlistKey, coverKey} {
		if !uploaded[key] {
			t.Errorf("recorded key %q is not among the uploaded keys %v", key, uploaded)
		}
	}

	if want := "processed/" + id.String() + "/master.m3u8"; playlistKey != want {
		t.Errorf("playlist key = %q, want %q", playlistKey, want)
	}
	if want := "processed/" + id.String() + "/thumbnails/cover.jpg"; coverKey != want {
		t.Errorf("cover key = %q, want %q", coverKey, want)
	}
}

func TestMarkProcessingWithNoRowIsPermanent(t *testing.T) {
	p := &pipeline{store: &fakeWorkerStore{processingErr: pgx.ErrNoRows}}

	err := p.process(context.Background(), uploadedObject{VideoID: uuid.MustParse(testVideoID)})
	if err == nil {
		t.Fatal("expected an error when the video row does not exist")
	}
	var perm *permanentError
	if !errors.As(err, &perm) {
		t.Fatalf("err = %v (%T), want a permanentError", err, err)
	}
}

func TestContentTypeFor(t *testing.T) {
	cases := map[string]string{
		"master.m3u8":      "application/vnd.apple.mpegurl",
		"segment_00000.ts": "video/mp2t",
		"cover.jpg":        "image/jpeg",
		"notes.txt":        "application/octet-stream",
	}
	for path, want := range cases {
		if got := contentTypeFor(path); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", path, got, want)
		}
	}
}
