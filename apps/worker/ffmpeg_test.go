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
	if _, err := parseProbe([]byte(`{"streams":[],"format":{"duration":"1.0"}}`)); err == nil {
		t.Fatal("expected an error when no video stream is present")
	}
}

func TestTranscodeArgsMatchProfile(t *testing.T) {
	args := strings.Join(transcodeArgs("/work/source.mp4", "/work/out"), " ")

	for _, want := range []string{
		"scale=1280:720:force_original_aspect_ratio=decrease",
		"-profile:v main", "-level:v 3.1",
		"-b:v 2800k", "-maxrate 3000k", "-bufsize 6000k",
		"keyint=180:min-keyint=180:scenecut=0:open-gop=0",
		"-b:a 128k", "-ar 48000", "-ac 2",
		"-hls_time 6", "-hls_playlist_type vod", "-hls_flags independent_segments",
		"/work/out/720/segment_%05d.ts",
		"/work/out/720/playlist.m3u8",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("transcode args missing %q\ngot: %s", want, args)
		}
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
