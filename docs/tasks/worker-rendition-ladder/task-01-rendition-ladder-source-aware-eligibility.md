# Task 1: Rendition ladder and source-aware eligibility

> Task 1 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Next: [Task 2](task-02-frame-rate-from-ffprobe-gop-length.md)

---

**Files:**
- Modify: `apps/worker/ffmpeg.go` (append declarations; the existing single-rendition constants stay until Task 4)
- Test: `apps/worker/ffmpeg_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type rendition struct { Dir string; Width, Height int32; Profile, Level string; VideoKbps, MaxrateKbps, BufsizeKbps, AudioKbps int; Codecs string }`
  - `func (r rendition) bandwidth() int`
  - `var ladder []rendition` — the four §2 entries, descending
  - `func eligibleRenditions(sourceHeight int32) []rendition`
  - `func upscaledFallback(sourceHeight int32) bool`

The `ladder` slice is the single place the §2 table lives. `bandwidth()` is `(maxrate + audio) × 1000`, which is §4's peak-bandwidth rule for `EXT-X-STREAM-INF`. `eligibleRenditions` returns entries in ladder (descending) order; the master playlist re-sorts ascending in Task 4. `upscaledFallback` is a separate predicate so the caller can log §3's `upscaled_fallback=true` flag without the selector returning a tuple.

Note on §5.3's example block: its 480p/720p/1080p `BANDWIDTH` values match the stated `maxrate + audio` rule exactly (1500+96, 3000+128, 5350+128), but its 360p line reads `935000` where the rule gives `850000 + 96000 = 946000`. The rule and the other three lines win; the example has an arithmetic slip, which Task 7 corrects in the spec.

- [ ] **Step 1: Write the failing tests**

Append to `apps/worker/ffmpeg_test.go`:

```go
func TestLadderIsDescendingAndMatchesTheProfileTable(t *testing.T) {
	if len(ladder) != 4 {
		t.Fatalf("ladder has %d entries, want the 4 defined in ffmpeg-profiles §2", len(ladder))
	}
	for i, r := range ladder {
		if r.BufsizeKbps != 2*r.MaxrateKbps {
			t.Errorf("%s: bufsize = %dk, want 2x maxrate (%dk)", r.Dir, r.BufsizeKbps, 2*r.MaxrateKbps)
		}
		if r.MaxrateKbps <= r.VideoKbps {
			t.Errorf("%s: maxrate %dk must exceed the target %dk", r.Dir, r.MaxrateKbps, r.VideoKbps)
		}
		if i == 0 {
			continue
		}
		prev := ladder[i-1]
		if r.Height >= prev.Height {
			t.Errorf("ladder must be descending by height: %s follows %s", r.Dir, prev.Dir)
		}
		if r.bandwidth() >= prev.bandwidth() {
			t.Errorf("ladder must be descending by bandwidth: %s follows %s", r.Dir, prev.Dir)
		}
	}
}

func TestRenditionBandwidthIsPeakVideoPlusAudio(t *testing.T) {
	want := map[string]int{"1080": 5478000, "720": 3128000, "480": 1596000, "360": 946000}
	for _, r := range ladder {
		if got := r.bandwidth(); got != want[r.Dir] {
			t.Errorf("%s bandwidth = %d, want %d", r.Dir, got, want[r.Dir])
		}
	}
}

func TestEligibleRenditionsNeverUpscale(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sourceHeight int32
		want         []string
		fallback     bool
	}{
		{"4k source", 2160, []string{"1080", "720", "480", "360"}, false},
		{"1080 source", 1080, []string{"1080", "720", "480", "360"}, false},
		{"720 source", 720, []string{"720", "480", "360"}, false},
		{"480 source", 480, []string{"480", "360"}, false},
		{"360 source", 360, []string{"360"}, false},
		{"240 source", 240, []string{"360"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := eligibleRenditions(tc.sourceHeight)
			dirs := make([]string, 0, len(got))
			for _, r := range got {
				dirs = append(dirs, r.Dir)
			}
			if !slices.Equal(dirs, tc.want) {
				t.Errorf("eligibleRenditions(%d) = %v, want %v", tc.sourceHeight, dirs, tc.want)
			}
			if got := upscaledFallback(tc.sourceHeight); got != tc.fallback {
				t.Errorf("upscaledFallback(%d) = %v, want %v", tc.sourceHeight, got, tc.fallback)
			}
		})
	}
}
```

`slices` is already imported by `ffmpeg_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run 'TestLadder|TestRenditionBandwidth|TestEligibleRenditions' -v`
Expected: FAIL to build — `undefined: ladder`, `undefined: eligibleRenditions`, `undefined: upscaledFallback`.

- [ ] **Step 3: Append the ladder and the selector to `apps/worker/ffmpeg.go`**

Add at the end of the file (leave everything above it untouched — the single-rendition constants are removed in Task 4):

```go
type rendition struct {
	Dir         string
	Width       int32
	Height      int32
	Profile     string
	Level       string
	VideoKbps   int
	MaxrateKbps int
	BufsizeKbps int
	AudioKbps   int
	Codecs      string
}

func (r rendition) bandwidth() int {
	return (r.MaxrateKbps + r.AudioKbps) * 1000
}

var ladder = []rendition{
	{
		Dir: "1080", Width: 1920, Height: 1080,
		Profile: "high", Level: "4.1",
		VideoKbps: 5000, MaxrateKbps: 5350, BufsizeKbps: 10700, AudioKbps: 128,
		Codecs: "avc1.640029,mp4a.40.2",
	},
	{
		Dir: "720", Width: 1280, Height: 720,
		Profile: "main", Level: "3.1",
		VideoKbps: 2800, MaxrateKbps: 3000, BufsizeKbps: 6000, AudioKbps: 128,
		Codecs: "avc1.4d001f,mp4a.40.2",
	},
	{
		Dir: "480", Width: 854, Height: 480,
		Profile: "main", Level: "3.0",
		VideoKbps: 1400, MaxrateKbps: 1500, BufsizeKbps: 3000, AudioKbps: 96,
		Codecs: "avc1.4d001e,mp4a.40.2",
	},
	{
		Dir: "360", Width: 640, Height: 360,
		Profile: "baseline", Level: "3.0",
		VideoKbps: 800, MaxrateKbps: 850, BufsizeKbps: 1700, AudioKbps: 96,
		Codecs: "avc1.42001e,mp4a.40.2",
	},
}

func eligibleRenditions(sourceHeight int32) []rendition {
	var eligible []rendition
	for _, r := range ladder {
		if r.Height <= sourceHeight {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		return []rendition{ladder[len(ladder)-1]}
	}
	return eligible
}

func upscaledFallback(sourceHeight int32) bool {
	return sourceHeight < ladder[len(ladder)-1].Height
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./apps/worker/... && gofmt -l . && go vet ./...`
Expected: PASS, `gofmt -l` prints nothing, `go vet` silent.

- [ ] **Step 5: Commit**

```bash
git add apps/worker/ffmpeg.go apps/worker/ffmpeg_test.go
git commit -m "feat: add the four-rendition ladder and source-aware rendition selection"
```

---
