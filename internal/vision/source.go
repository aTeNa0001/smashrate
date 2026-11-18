package vision

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // saved appearances are JPEG
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// FrameSource yields frames until it is exhausted.
type FrameSource interface {
	Next() (image.Image, error)
	Close() error
}

// FFmpegSource reads raw RGB frames from an ffmpeg subprocess attached to
// the capture board. ffmpeg handles every OS/device quirk; we only read bytes.
type FFmpegSource struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	w, h   int
	cancel context.CancelFunc
}

// OpenFFmpeg starts ffmpeg for the configured device.
func OpenFFmpeg(cfg config.VisionConfig) (*FFmpegSource, error) {
	w, h := cfg.Width, cfg.Height
	if w <= 0 || h <= 0 {
		w, h = 640, 360
	}
	w &^= 1 // yuyv packs two pixels per sample
	fps := cfg.FPS
	if fps <= 0 {
		fps = 2
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-f", cfg.InputFormat}
	if cfg.InputFormat == "dshow" {
		// We only consume a few frames per second; a bigger real-time buffer
		// stops dshow from logging "buffer too full, frame dropped" constantly.
		args = append(args, "-rtbufsize", "256M")
	}
	args = append(args, cfg.InputArgs...)
	args = append(args, "-i", cfg.Input,
		"-vf", fmt.Sprintf("fps=%g,scale=%d:%d", fps, w, h),
		"-f", "rawvideo", "-pix_fmt", "yuyv422", "-")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cfg.FFmpeg, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &FFmpegSource{cmd: cmd, out: out, w: w, h: h, cancel: cancel}, nil
}

// Next blocks for the next frame. ffmpeg passes the board's packed YUYV
// through untouched (see YUYV); converting every pixel to RGB in ffmpeg
// cost a whole core at 1080p30.
func (s *FFmpegSource) Next() (image.Image, error) {
	img := NewYUYV(s.w, s.h)
	if _, err := io.ReadFull(s.out, img.Pix); err != nil {
		return nil, err
	}
	return img, nil
}

// Close stops ffmpeg.
func (s *FFmpegSource) Close() error {
	s.cancel()
	_ = s.cmd.Wait()
	return nil
}

// FileSource replays image files (PNG/JPEG); used for tests and for tuning
// the layout from saved screenshots.
type FileSource struct {
	paths []string
	i     int
}

// OpenFiles creates a FileSource.
func OpenFiles(paths ...string) *FileSource { return &FileSource{paths: paths} }

// Next decodes the next file; io.EOF when exhausted.
func (s *FileSource) Next() (image.Image, error) {
	if s.i >= len(s.paths) {
		return nil, io.EOF
	}
	p := s.paths[s.i]
	s.i++
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return img, nil
}

// Close is a no-op.
func (s *FileSource) Close() error { return nil }

// SavePNG writes img to path.
func SavePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, toRGBA(img))
}

// Crop returns a copy of region r of img.
func Crop(img image.Image, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			cr, cg, cb := rgbAt(img, r.Min.X+x, r.Min.Y+y)
			i := out.PixOffset(x, y)
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = cr, cg, cb, 255
		}
	}
	return out
}

// ListDevices runs ffmpeg's device listing for the configured input format
// and returns its raw output (ffmpeg prints it on stderr).
func ListDevices(cfg config.VisionConfig) string {
	var args []string
	switch cfg.InputFormat {
	case "dshow":
		args = []string{"-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy"}
	case "avfoundation":
		args = []string{"-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
	default:
		return "device listing: run `v4l2-ctl --list-devices` (Linux) or check ffmpeg docs for " + strconv.Quote(cfg.InputFormat)
	}
	out, _ := exec.Command(cfg.FFmpeg, args...).CombinedOutput()
	return string(out)
}
