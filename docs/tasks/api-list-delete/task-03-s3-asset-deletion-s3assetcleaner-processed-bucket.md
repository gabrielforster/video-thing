# Task 3: S3 asset deletion (`S3AssetCleaner`) and `PROCESSED_BUCKET` config

> Task 3 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 2](task-02-get-videos-pagination.md) · Next: [Task 4](task-04-delete-videos-id-handler.md)

---

**Files:**
- Create: `apps/api/assets.go`
- Create: `apps/api/assets_test.go`
- Modify: `apps/api/config.go`
- Modify: `apps/api/config_test.go`

**Interfaces:**
- Consumes: `db.Video` (vertical slice); AWS SDK v2 `s3.Client` methods `ListObjectsV2`, `DeleteObjects` (signatures fixed by the SDK, `github.com/aws/aws-sdk-go-v2/service/s3` v1.106.0, already a dependency).
- Produces: `type s3API interface { ListObjectsV2(...); DeleteObjects(...) }`; `type S3AssetCleaner struct`; `func NewS3AssetCleaner(client s3API, processedBucket string) *S3AssetCleaner`; `func (a *S3AssetCleaner) deleteVideoAssets(ctx context.Context, v db.Video) error` (this becomes the `assetCleaner` interface's one method in Task 4); `Config.ProcessedBucket`.

This task does not wire the cleaner into `handlers` yet — that's Task 4, once `DeleteVideo` exists on the `store` interface. This task only builds and unit-tests the S3 logic in isolation, the same way `apps/worker/consumer.go`'s `sqsAPI` interface is faked in `consumer_test.go` without touching a real queue.

`sequence-diagrams.md`'s "Deletion Flow" depicts both buckets being cleaned via `ListObjectsV2` + `DeleteObjects` on a `{id}/` prefix. This plan deviates for the **raw** bucket only: since exactly one raw object exists per video (`source_key`, from the already-deleted row), no listing is needed there — `deleteBatch` is called directly with that single key. The **processed** bucket still needs listing, since ffmpeg produces a variable number of segment files.

`PROCESSED_BUCKET` is already exported by the `Makefile` (line 10, `export PROCESSED_BUCKET ?= video-thing-dev-processed-assets`) for the worker, and by `scripts/e2e.sh` (line 13). No changes are needed in either file — `apps/api` will now simply also read the variable that was already being exported to its environment.

- [ ] **Step 1: Write the failing S3-cleanup tests**

Create `apps/api/assets_test.go`:

```go
package main

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

type fakeS3 struct {
	pages       [][]string
	pageIndex   int
	listErr     error
	deleteErr   error
	deleteCalls [][]string
}

func (f *fakeS3) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.pageIndex >= len(f.pages) {
		return &s3.ListObjectsV2Output{}, nil
	}
	keys := f.pages[f.pageIndex]
	f.pageIndex++
	contents := make([]types.Object, len(keys))
	for i, k := range keys {
		contents[i] = types.Object{Key: aws.String(k)}
	}
	out := &s3.ListObjectsV2Output{Contents: contents}
	if f.pageIndex < len(f.pages) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String("token-" + strconv.Itoa(f.pageIndex))
	}
	return out, nil
}

func (f *fakeS3) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	keys := make([]string, len(in.Delete.Objects))
	for i, obj := range in.Delete.Objects {
		keys[i] = aws.ToString(obj.Key)
	}
	f.deleteCalls = append(f.deleteCalls, keys)
	return &s3.DeleteObjectsOutput{}, nil
}

func TestDeleteVideoAssetsDeletesRawKeyThenProcessedPrefix(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	processedKeys := []string{"processed/" + id.String() + "/master.m3u8", "processed/" + id.String() + "/720/playlist.m3u8"}

	fake := &fakeS3{pages: [][]string{processedKeys}}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err != nil {
		t.Fatalf("deleteVideoAssets: %v", err)
	}

	if len(fake.deleteCalls) != 2 {
		t.Fatalf("len(deleteCalls) = %d, want 2", len(fake.deleteCalls))
	}
	if len(fake.deleteCalls[0]) != 1 || fake.deleteCalls[0][0] != video.SourceKey {
		t.Fatalf("first DeleteObjects call = %v, want [%s]", fake.deleteCalls[0], video.SourceKey)
	}
	if len(fake.deleteCalls[1]) != len(processedKeys) {
		t.Fatalf("second DeleteObjects call = %v, want %v", fake.deleteCalls[1], processedKeys)
	}
}

func TestDeleteVideoAssetsBatchesDeleteObjectsAt1000Keys(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}

	var page1, page2 []string
	for i := 0; i < 1000; i++ {
		page1 = append(page1, "processed/"+id.String()+"/"+strconv.Itoa(i))
	}
	for i := 1000; i < 1500; i++ {
		page2 = append(page2, "processed/"+id.String()+"/"+strconv.Itoa(i))
	}

	fake := &fakeS3{pages: [][]string{page1, page2}}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err != nil {
		t.Fatalf("deleteVideoAssets: %v", err)
	}

	if len(fake.deleteCalls) != 3 {
		t.Fatalf("len(deleteCalls) = %d, want 3 (1 raw + 2 processed batches)", len(fake.deleteCalls))
	}
	if len(fake.deleteCalls[1]) != 1000 {
		t.Fatalf("first processed batch = %d keys, want 1000", len(fake.deleteCalls[1]))
	}
	if len(fake.deleteCalls[2]) != 500 {
		t.Fatalf("second processed batch = %d keys, want 500", len(fake.deleteCalls[2]))
	}
}

func TestDeleteVideoAssetsPropagatesListError(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	fake := &fakeS3{listErr: errors.New("list boom")}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err == nil {
		t.Fatal("expected an error when ListObjectsV2 fails")
	}
}

func TestDeleteVideoAssetsPropagatesDeleteError(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	fake := &fakeS3{deleteErr: errors.New("delete boom")}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err == nil {
		t.Fatal("expected an error when DeleteObjects fails")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./apps/api/... -run TestDeleteVideoAssets -v`
Expected: FAIL to compile — `undefined: NewS3AssetCleaner`.

- [ ] **Step 3: Write `apps/api/assets.go`**

```go
package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const maxDeleteBatch = 1000

type s3API interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type S3AssetCleaner struct {
	s3              s3API
	processedBucket string
}

func NewS3AssetCleaner(client s3API, processedBucket string) *S3AssetCleaner {
	return &S3AssetCleaner{s3: client, processedBucket: processedBucket}
}

func (a *S3AssetCleaner) deleteVideoAssets(ctx context.Context, v db.Video) error {
	if err := a.deleteBatch(ctx, v.SourceBucket, []string{v.SourceKey}); err != nil {
		return fmt.Errorf("delete raw object: %w", err)
	}

	prefix := "processed/" + v.ID.String() + "/"
	keys, err := a.listKeys(ctx, a.processedBucket, prefix)
	if err != nil {
		return fmt.Errorf("list processed objects: %w", err)
	}
	if err := a.deleteBatch(ctx, a.processedBucket, keys); err != nil {
		return fmt.Errorf("delete processed objects: %w", err)
	}
	return nil
}

func (a *S3AssetCleaner) listKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(a.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}
	return keys, nil
}

func (a *S3AssetCleaner) deleteBatch(ctx context.Context, bucket string, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), maxDeleteBatch)
		batch := keys[:n]
		keys = keys[n:]

		ids := make([]types.ObjectIdentifier, len(batch))
		for i, k := range batch {
			ids[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		if _, err := a.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids},
		}); err != nil {
			return err
		}
	}
	return nil
}
```

`s3.NewListObjectsV2Paginator` takes an `s3.ListObjectsV2APIClient` (one method: `ListObjectsV2`); `s3API` has a superset of that method set with an identical signature, so `a.s3` (typed `s3API`) is directly assignable — no adapter needed. This was verified to compile and to paginate correctly (the paginator reads `IsTruncated`/`NextContinuationToken` off `*s3.ListObjectsV2Output`, which is exactly what `fakeS3` sets).

- [ ] **Step 4: Add `ProcessedBucket` to `Config`**

Modify `apps/api/config.go`. The full file, after this step:

```go
package main

import (
	"fmt"
	"sort"
	"strings"
)

type Config struct {
	DatabaseURL        string
	RawBucket          string
	ProcessedBucket    string
	AWSEndpointURL     string
	PublicAssetBaseURL string
	Port               string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:        getenv("DATABASE_URL"),
		RawBucket:          getenv("RAW_BUCKET"),
		ProcessedBucket:    getenv("PROCESSED_BUCKET"),
		AWSEndpointURL:     getenv("AWS_ENDPOINT_URL"),
		PublicAssetBaseURL: strings.TrimSuffix(getenv("PUBLIC_ASSET_BASE_URL"), "/"),
		Port:               getenv("PORT"),
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":          cfg.DatabaseURL,
		"RAW_BUCKET":            cfg.RawBucket,
		"PROCESSED_BUCKET":      cfg.ProcessedBucket,
		"PUBLIC_ASSET_BASE_URL": cfg.PublicAssetBaseURL,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
```

Update `apps/api/config_test.go` to match — the full file, after this step:

```go
package main

import "testing"
import "strings"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigDefaultsPort(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"DATABASE_URL":          "postgres://localhost/db",
		"RAW_BUCKET":            "raw",
		"PROCESSED_BUCKET":      "processed",
		"PUBLIC_ASSET_BASE_URL": "http://localhost:4566/processed",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.AWSEndpointURL != "" {
		t.Fatalf("AWSEndpointURL = %q, want empty", cfg.AWSEndpointURL)
	}
}

func TestLoadConfigRequiresVars(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"RAW_BUCKET": "raw"}))
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if got := err.Error(); !strings.Contains(got, "DATABASE_URL") || !strings.Contains(got, "PUBLIC_ASSET_BASE_URL") || !strings.Contains(got, "PROCESSED_BUCKET") {
		t.Fatalf("error %q should name every missing variable", got)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./apps/api/... -v`
Expected: PASS for all tests, including the 4 new `TestDeleteVideoAssets*` tests and the updated config tests.

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add apps/api/assets.go apps/api/assets_test.go apps/api/config.go apps/api/config_test.go
git commit -m "feat: add S3AssetCleaner and PROCESSED_BUCKET config"
```

---
