package main

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

const sampleEvent = `{
  "Records": [
    {
      "eventName": "ObjectCreated:Put",
      "s3": {
        "bucket": {"name": "video-thing-dev-raw-uploads"},
        "object": {"key": "raw/3fa85f64-5717-4562-b3fc-2c963f66afa6", "size": 52428800}
      }
    }
  ]
}`

func TestParseUploadExtractsVideoID(t *testing.T) {
	got, err := parseUpload(sampleEvent)
	if err != nil {
		t.Fatalf("parseUpload: %v", err)
	}
	if got.VideoID != uuid.MustParse("3fa85f64-5717-4562-b3fc-2c963f66afa6") {
		t.Fatalf("VideoID = %s", got.VideoID)
	}
	if got.Bucket != "video-thing-dev-raw-uploads" {
		t.Fatalf("Bucket = %q", got.Bucket)
	}
	if got.Key != "raw/3fa85f64-5717-4562-b3fc-2c963f66afa6" {
		t.Fatalf("Key = %q", got.Key)
	}
	if got.Size != 52428800 {
		t.Fatalf("Size = %d", got.Size)
	}
}

func TestParseUploadUnescapesKey(t *testing.T) {
	body := `{"Records":[{"s3":{"bucket":{"name":"b"},"object":{"key":"raw%2F3fa85f64-5717-4562-b3fc-2c963f66afa6","size":1}}}]}`
	got, err := parseUpload(body)
	if err != nil {
		t.Fatalf("parseUpload: %v", err)
	}
	if got.Key != "raw/3fa85f64-5717-4562-b3fc-2c963f66afa6" {
		t.Fatalf("Key = %q, want unescaped", got.Key)
	}
}

func TestParseUploadRejectsNonUploads(t *testing.T) {
	cases := map[string]string{
		"s3 test event": `{"Service":"Amazon S3","Event":"s3:TestEvent"}`,
		"not json":      `hello`,
		"empty records": `{"Records":[]}`,
		"wrong prefix":  `{"Records":[{"s3":{"bucket":{"name":"b"},"object":{"key":"other/3fa85f64-5717-4562-b3fc-2c963f66afa6"}}}]}`,
		"not a uuid":    `{"Records":[{"s3":{"bucket":{"name":"b"},"object":{"key":"raw/not-a-uuid"}}}]}`,
		"nested key":    `{"Records":[{"s3":{"bucket":{"name":"b"},"object":{"key":"raw/a/3fa85f64-5717-4562-b3fc-2c963f66afa6"}}}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUpload(body); !errors.Is(err, errNotAnUpload) {
				t.Fatalf("err = %v, want errNotAnUpload", err)
			}
		})
	}
}
