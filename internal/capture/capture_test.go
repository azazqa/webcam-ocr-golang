package capture

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"webcam-ocr/internal/extract"
)

type fakeSrc struct {
	mu     sync.Mutex
	frames []image.Image
	i      int
	errN   int // fail this many times then succeed; if <0 always fail
	fail   error
}

func (f *fakeSrc) Grab() (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errN < 0 {
		if f.fail != nil {
			return nil, f.fail
		}
		return nil, errors.New("grab failed")
	}
	if f.errN > 0 {
		f.errN--
		return nil, errors.New("transient")
	}
	if f.i >= len(f.frames) {
		// hold last frame
		if len(f.frames) == 0 {
			return nil, errors.New("no frames")
		}
		time.Sleep(5 * time.Millisecond)
		return f.frames[len(f.frames)-1], nil
	}
	img := f.frames[f.i]
	f.i++
	time.Sleep(5 * time.Millisecond)
	return img, nil
}

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func pageA() *image.RGBA {
	img := solid(320, 180, color.RGBA{240, 240, 240, 255})
	for y := 40; y < 50; y++ {
		for x := 20; x < 280; x++ {
			img.Set(x, y, color.RGBA{20, 20, 20, 255})
		}
	}
	return img
}

func pageB() *image.RGBA {
	img := solid(320, 180, color.RGBA{240, 240, 240, 255})
	for y := 80; y < 140; y++ {
		for x := 40; x < 200; x++ {
			img.Set(x, y, color.RGBA{10, 10, 10, 255})
		}
	}
	return img
}

func TestRunIdleStop(t *testing.T) {
	dir := t.TempDir()
	a := pageA()
	frames := make([]image.Image, 0, 40)
	for i := 0; i < 40; i++ {
		frames = append(frames, a)
	}
	src := &fakeSrc{frames: frames}
	s, err := Start(src, Options{
		BaseDir:     dir,
		IdleTimeout: 400 * time.Millisecond,
		Detect: extract.Config{
			StableDuration:  50 * time.Millisecond,
			Lookback:        40 * time.Millisecond,
			MinStableFrames: 2,
		},
		DeviceName: "fake",
		Format:     "320x180",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Dir() != "" {
		t.Fatal("Dir should be empty before Run")
	}
	res := s.Run(context.Background())
	if res.Reason != StopByIdle {
		t.Fatalf("reason=%v err=%v", res.Reason, res.Err)
	}
	if s.Dir() == "" {
		t.Fatal("Dir empty after Run")
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "session.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "capture_log.csv")); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	b, _ := os.ReadFile(filepath.Join(s.Dir(), "session.json"))
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["stopReason"] != "idle" {
		t.Fatalf("session.json stopReason=%v", meta["stopReason"])
	}
}

func TestManualAndSceneChange(t *testing.T) {
	dir := t.TempDir()
	a, b := pageA(), pageB()
	frames := []image.Image{}
	for i := 0; i < 15; i++ {
		frames = append(frames, a)
	}
	for i := 0; i < 20; i++ {
		frames = append(frames, b)
	}
	src := &fakeSrc{frames: frames}
	s, err := Start(src, Options{
		BaseDir: dir,
		Detect: extract.Config{
			StableDuration:  40 * time.Millisecond,
			Lookback:        30 * time.Millisecond,
			MinStableFrames: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.ManualCapture(); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
	cancel()
	res := <-done
	if res.Reason != StopByCancel {
		t.Fatalf("reason=%v err=%v count=%d", res.Reason, res.Err, res.Count)
	}
	if res.Count < 1 {
		t.Fatalf("expected captures, got %d", res.Count)
	}

	f, err := os.Open(filepath.Join(s.Dir(), "capture_log.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	bom := make([]byte, 3)
	if _, err := f.Read(bom); err != nil || bom[0] != 0xEF {
		t.Fatalf("missing BOM: %v %v", bom, err)
	}
	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	hasManual := false
	for _, row := range rows[1:] {
		if len(row) >= 6 && row[5] == "수동" {
			hasManual = true
		}
		png := filepath.Join(s.Dir(), row[1])
		if _, err := os.Stat(png); err != nil {
			t.Fatalf("missing png %s: %v", row[1], err)
		}
	}
	if !hasManual {
		t.Fatal("expected manual row")
	}
}

func TestGrabFailStop(t *testing.T) {
	dir := t.TempDir()
	src := &fakeSrc{errN: -1, fail: errors.New("camera dead")}
	s, err := Start(src, Options{BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	res := s.Run(context.Background())
	if res.Reason != StopByError {
		t.Fatalf("reason=%v", res.Reason)
	}
}

func TestFormatElapsedFile(t *testing.T) {
	got := formatElapsedFile(125*time.Minute + 3*time.Second + 100*time.Millisecond)
	if !strings.HasPrefix(got, "t125m") {
		t.Fatalf("got %s", got)
	}
}

func TestManualBeforeFrame(t *testing.T) {
	dir := t.TempDir()
	block := make(chan struct{})
	src := &blockingSrc{block: block}
	s, err := Start(src, Options{BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	err = s.ManualCapture()
	close(block)
	s.Stop()
	if err == nil || !strings.Contains(err.Error(), "no frame") {
		t.Fatalf("want no frame error, got %v", err)
	}
}

type blockingSrc struct {
	block chan struct{}
	once  sync.Once
}

func (b *blockingSrc) Grab() (image.Image, error) {
	<-b.block
	return pageA(), nil
}
