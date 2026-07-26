package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
)

const (
	renditionDir       = "720"
	renditionBandwidth = 3128000
	renditionCodecs    = "avc1.4d001f,mp4a.40.2"
	renditionWidth     = 1280
	renditionHeight    = 720
	gopFrames          = 180
	segmentSeconds     = 6
)

type probeResult struct {
	Width    int32
	Height   int32
	Duration float64
}

func probeArgs(src string) []string {
	return []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate,codec_name,bit_rate",
		"-show_entries", "format=duration,bit_rate",
		"-of", "json",
		src,
	}
}

func parseProbe(stdout []byte) (probeResult, error) {
	var raw struct {
		Streams []struct {
			Width  int32 `json:"width"`
			Height int32 `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return probeResult{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(raw.Streams) == 0 || raw.Streams[0].Height == 0 {
		return probeResult{}, errors.New("source has no video stream")
	}

	duration, err := strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil {
		return probeResult{}, fmt.Errorf("parse duration %q: %w", raw.Format.Duration, err)
	}

	return probeResult{
		Width:    raw.Streams[0].Width,
		Height:   raw.Streams[0].Height,
		Duration: duration,
	}, nil
}

func transcodeArgs(src, outDir string) []string {
	dir := filepath.Join(outDir, renditionDir)
	return []string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			renditionWidth, renditionHeight, renditionWidth, renditionHeight),
		"-c:v", "libx264",
		// yuv420p is mandatory, not a default: -profile:v main cannot encode a
		// 4:2:2/4:4:4 or 10-bit source (ProRes, many screen recorders, HDR
		// phone captures, ffmpeg's own testsrc), and libx264 refuses the whole
		// encode rather than converting on its own.
		"-pix_fmt", "yuv420p",
		"-profile:v", "main", "-level:v", "3.1",
		"-b:v", "2800k", "-maxrate", "3000k", "-bufsize", "6000k",
		"-r", "30",
		"-x264-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:open-gop=0", gopFrames, gopFrames),
		"-c:a", "aac", "-profile:a", "aac_low", "-b:a", "128k", "-ar", "48000", "-ac", "2",
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "segment_%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	}
}

func coverArgs(src, out string, duration float64) []string {
	seek := duration * 0.10
	if seek < 1 {
		seek = 1
	}
	return []string{
		"-y",
		"-ss", strconv.FormatFloat(seek, 'f', 3, 64),
		"-i", src,
		"-vframes", "1",
		"-q:v", "2",
		"-vf", "scale=1280:-2",
		out,
	}
}

func masterPlaylist() string {
	return fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q\n%s/playlist.m3u8\n",
		renditionBandwidth, renditionWidth, renditionHeight, renditionCodecs, renditionDir)
}
