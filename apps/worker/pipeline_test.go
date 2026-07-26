package main

import (
	"testing"

	"github.com/google/uuid"
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
