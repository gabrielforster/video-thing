package main

import (
	"strings"
	"testing"
)

const sampleProbe = `{
  "streams": [
    {"codec_name": "h264", "width": 1920, "height": 1080, "r_frame_rate": "30000/1001"}
  ],
  "format": {"duration": "184.522000", "bit_rate": "6127412"}
}`

func TestParseProbe(t *testing.T) {
	got, err := parseProbe([]byte(sampleProbe))
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if got.Width != 1920 || got.Height != 1080 {
		t.Fatalf("dimensions = %dx%d, want 1920x1080", got.Width, got.Height)
	}
	if got.Duration != 184.522 {
		t.Fatalf("duration = %v, want 184.522", got.Duration)
	}
}

func TestParseProbeRejectsSourceWithoutVideoStream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{"no streams", `{"streams":[],"format":{"duration":"1.0"}}`},
		{"stream without dimensions", `{"streams":[{"codec_name":"h264","width":0,"height":0}],"format":{"duration":"1.0"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseProbe([]byte(tc.stdout)); err == nil {
				t.Fatal("expected an error when no video stream is present")
			}
		})
	}
}

func TestParseProbeRejectsUnparseableDuration(t *testing.T) {
	stdout := `{"streams":[{"width":1920,"height":1080}],"format":{"duration":"N/A"}}`
	if _, err := parseProbe([]byte(stdout)); err == nil {
		t.Fatal("expected an error when the duration is not a number")
	}
}

func argValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg != flag {
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("flag %q is last in the arg list, so it has no value\ngot: %v", flag, args)
		}
		return args[i+1]
	}
	t.Fatalf("arg list is missing flag %q\ngot: %v", flag, args)
	return ""
}

func TestTranscodeArgsMatchProfile(t *testing.T) {
	args := transcodeArgs("/work/source.mp4", "/work/out")

	for flag, want := range map[string]string{
		"-i":                    "/work/source.mp4",
		"-vf":                   "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2",
		"-c:v":                  "libx264",
		"-profile:v":            "main",
		"-level:v":              "3.1",
		"-b:v":                  "2800k",
		"-maxrate":              "3000k",
		"-bufsize":              "6000k",
		"-r":                    "30",
		"-x264-params":          "keyint=180:min-keyint=180:scenecut=0:open-gop=0",
		"-c:a":                  "aac",
		"-profile:a":            "aac_low",
		"-b:a":                  "128k",
		"-ar":                   "48000",
		"-ac":                   "2",
		"-f":                    "hls",
		"-hls_time":             "6",
		"-hls_playlist_type":    "vod",
		"-hls_flags":            "independent_segments",
		"-hls_segment_filename": "/work/out/720/segment_%05d.ts",
	} {
		if got := argValue(t, args, flag); got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}

	if got := args[len(args)-1]; got != "/work/out/720/playlist.m3u8" {
		t.Errorf("output playlist = %q, want %q", got, "/work/out/720/playlist.m3u8")
	}
}

func TestCoverArgsSeekToTenPercentClampedToOneSecond(t *testing.T) {
	long := strings.Join(coverArgs("/work/source.mp4", "/work/cover.jpg", 184.522), " ")
	if !strings.Contains(long, "-ss 18.452") {
		t.Errorf("long clip should seek to 10%%\ngot: %s", long)
	}

	short := strings.Join(coverArgs("/work/source.mp4", "/work/cover.jpg", 5), " ")
	if !strings.Contains(short, "-ss 1.000") {
		t.Errorf("short clip should clamp to 1s\ngot: %s", short)
	}
}

func TestMasterPlaylistListsTheOneRendition(t *testing.T) {
	want := "#EXTM3U\n" +
		"#EXT-X-VERSION:3\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3128000,RESOLUTION=1280x720,CODECS=\"avc1.4d001f,mp4a.40.2\"\n" +
		"720/playlist.m3u8\n"
	if got := masterPlaylist(); got != want {
		t.Fatalf("master playlist =\n%q\nwant\n%q", got, want)
	}
}
