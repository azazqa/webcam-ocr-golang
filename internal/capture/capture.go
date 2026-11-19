// Package capture runs work A: webcam frames → scene-change detection → PNG session folder.
package capture

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"webcam-ocr/internal/extract"
	"webcam-ocr/internal/imgconv"
)

const (
	writerBufSize = 8
	grabFailLimit = 10
	grabJoinWait  = 2 * time.Second
	idleTick      = 200 * time.Millisecond
)

// FrameSource supplies frames (e.g. *camera.Camera).
type FrameSource interface {
	Grab() (image.Image, error)
}

// Options configures a capture session.
type Options struct {
	BaseDir     string
	Detect      extract.Config
	IdleTimeout time.Duration // 0 = auto-stop disabled
	DeviceName  string
	Format      string
}

// StopReason explains why Run returned.
type StopReason int

const (
	StopByUser StopReason = iota
	StopByIdle
	StopByError
	StopByCancel
)

func (r StopReason) String() string {
	switch r {
	case StopByUser:
		return "user"
	case StopByIdle:
		return "idle"
	case StopByError:
		return "error"
	case StopByCancel:
		return "cancel"
	default:
		return fmt.Sprintf("StopReason(%d)", int(r))
	}
}

// Result is returned after Run finishes draining the writer.
type Result struct {
	Count  int
	Reason StopReason
	Err    error
}

type frameSlot struct {
	img    image.Image
	recvAt time.Duration
	seq    uint64
}

type saveJob struct {
	img       image.Image
	name      string
	seq       int
	elapsed   time.Duration
	wall      time.Time
	diffRatio float64
	manual    bool
	hasDiff   bool
}

// Session is one capture run (folder created at Run start).
type Session struct {
	src  FrameSource
	opts Options

	mu      sync.Mutex
	dir     string
	count   int
	started time.Time
	running bool

	stopCh   chan struct{}
	manualCh chan chan error
	stopped  atomic.Bool

	lastChangeNs atomic.Int64 // session-relative last change, for Stats
}

// Start validates options and prepares a Session. Does not create the session folder.
func Start(src FrameSource, opts Options) (*Session, error) {
	if src == nil {
		return nil, errors.New("capture: nil FrameSource")
	}
	if opts.BaseDir == "" {
		return nil, errors.New("capture: BaseDir required")
	}
	if err := os.MkdirAll(opts.BaseDir, 0o755); err != nil {
		return nil, fmt.Errorf("capture: BaseDir: %w", err)
	}
	probe := filepath.Join(opts.BaseDir, ".write_probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("capture: BaseDir not writable: %w", err)
	}
	_ = os.Remove(probe)

	return &Session{
		src:      src,
		opts:     opts,
		stopCh:   make(chan struct{}),
		manualCh: make(chan chan error, 1),
	}, nil
}

// Dir returns the session folder, or "" before Run creates it.
func (s *Session) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// Stop requests StopByUser.
func (s *Session) Stop() {
	if s.stopped.CompareAndSwap(false, true) {
		close(s.stopCh)
	}
}

// ManualCapture requests saving the latest frame. Errors if not running or no frame yet.
func (s *Session) ManualCapture() error {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return errors.New("capture: not running")
	}
	done := make(chan error, 1)
	select {
	case s.manualCh <- done:
	case <-time.After(5 * time.Second):
		return errors.New("capture: manual capture timeout")
	}
	return <-done
}

// Stats returns progress. idleOK is false when auto-stop is disabled or not running.
func (s *Session) Stats() (count int, elapsed, idleRemaining time.Duration, idleOK bool) {
	s.mu.Lock()
	count = s.count
	started := s.started
	running := s.running
	idleTO := s.opts.IdleTimeout
	s.mu.Unlock()
	if !started.IsZero() {
		elapsed = time.Since(started)
	}
	if !running || idleTO <= 0 {
		return count, elapsed, 0, false
	}
	lc := time.Duration(s.lastChangeNs.Load())
	used := elapsed - lc
	if used < 0 {
		used = 0
	}
	rem := idleTO - used
	if rem < 0 {
		rem = 0
	}
	return count, elapsed, rem, true
}

// Run creates the session folder and runs grab/detect/writer until stop.
func (s *Session) Run(ctx context.Context) Result {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return Result{Reason: StopByError, Err: errors.New("capture: already running")}
	}
	s.running = true
	s.started = time.Now()
	s.count = 0
	s.stopped.Store(false)
	s.stopCh = make(chan struct{})
	name := s.started.Format("20060102_150405")
	dir := filepath.Join(s.opts.BaseDir, name)
	s.dir = dir
	started := s.started
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{Reason: StopByError, Err: err}
	}
	det := extract.NewDetector(s.opts.Detect)
	if err := writeSessionJSON(dir, s.opts, started, nil); err != nil {
		return Result{Reason: StopByError, Err: err}
	}

	csvFile, err := os.Create(filepath.Join(dir, "capture_log.csv"))
	if err != nil {
		return Result{Reason: StopByError, Err: err}
	}
	if _, err := csvFile.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		csvFile.Close()
		return Result{Reason: StopByError, Err: err}
	}
	cw := csv.NewWriter(csvFile)
	_ = cw.Write([]string{"seq", "filename", "captured_at", "elapsed", "diff_ratio", "mode"})

	jobs := make(chan saveJob, writerBufSize)
	var writerErr atomic.Pointer[error]
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for job := range jobs {
			path := filepath.Join(dir, job.name)
			if err := imgconv.SavePNG(job.img, path); err != nil {
				e := err
				writerErr.Store(&e)
				continue
			}
			diff := ""
			if job.hasDiff {
				diff = fmt.Sprintf("%.4f", job.diffRatio)
			}
			mode := "자동"
			if job.manual {
				mode = "수동"
			}
			_ = cw.Write([]string{
				fmt.Sprintf("%d", job.seq),
				job.name,
				job.wall.Format("2006-01-02 15:04:05.000"),
				formatElapsed(job.elapsed),
				diff,
				mode,
			})
			cw.Flush()
		}
	}()

	var (
		slotMu  sync.Mutex
		slot    frameSlot
		hasSlot bool
	)
	grabDone := make(chan struct{})
	grabFail := make(chan error, 1)

	go func() {
		defer close(grabDone)
		var seq uint64
		fails := 0
		for {
			select {
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			default:
			}
			img, err := s.src.Grab()
			if err != nil {
				fails++
				if fails >= grabFailLimit {
					select {
					case grabFail <- err:
					default:
					}
					return
				}
				time.Sleep(20 * time.Millisecond)
				continue
			}
			fails = 0
			seq++
			recvAt := time.Since(started)
			slotMu.Lock()
			slot = frameSlot{img: img, recvAt: recvAt, seq: seq}
			hasSlot = true
			slotMu.Unlock()
		}
	}()

	reason := StopByUser
	var runErr error
	var lastSeq uint64
	ticker := time.NewTicker(idleTick)
	defer ticker.Stop()

	allocSeq := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.count++
		return s.count
	}

	enqueue := func(job saveJob) bool {
		select {
		case jobs <- job:
			return true
		case <-s.stopCh:
			return false
		case <-ctx.Done():
			return false
		}
	}

	nextName := func(seq int, elapsed time.Duration) string {
		return fmt.Sprintf("%04d_%s.png", seq, formatElapsedFile(elapsed))
	}

	updateIdle := func() {
		s.lastChangeNs.Store(int64(det.LastChange()))
	}
	updateIdle()

loop:
	for {
		if e := writerErr.Load(); e != nil {
			reason, runErr = StopByError, *e
			break loop
		}
		select {
		case <-s.stopCh:
			reason = StopByUser
			break loop
		case <-ctx.Done():
			reason = StopByCancel
			runErr = ctx.Err()
			break loop
		case err := <-grabFail:
			reason, runErr = StopByError, err
			break loop
		case done := <-s.manualCh:
			slotMu.Lock()
			ok := hasSlot
			fr := slot
			slotMu.Unlock()
			if !ok {
				done <- errors.New("capture: no frame yet")
				continue
			}
			seq := allocSeq()
			dr := det.MarkSaved(fr.img, fr.recvAt)
			job := saveJob{
				img: fr.img, name: nextName(seq, fr.recvAt), seq: seq,
				elapsed: fr.recvAt, wall: started.Add(fr.recvAt),
				diffRatio: dr, manual: true, hasDiff: true,
			}
			if !enqueue(job) {
				done <- errors.New("capture: stopping")
				reason = StopByUser
				break loop
			}
			done <- nil
		case <-ticker.C:
			now := time.Since(started)
			updateIdle()
			if s.opts.IdleTimeout > 0 {
				base := det.LastChange()
				if now-base >= s.opts.IdleTimeout {
					reason = StopByIdle
					break loop
				}
			}

			slotMu.Lock()
			ok := hasSlot
			fr := slot
			slotMu.Unlock()
			if !ok || fr.seq == lastSeq {
				continue
			}
			lastSeq = fr.seq
			if cap, hit := det.Feed(fr.img, fr.recvAt); hit {
				seq := allocSeq()
				job := saveJob{
					img: cap.Image, name: nextName(seq, cap.Time), seq: seq,
					elapsed: cap.Time, wall: started.Add(cap.Time),
					diffRatio: cap.DiffRatio, manual: false, hasDiff: true,
				}
				if !enqueue(job) {
					reason = StopByUser
					break loop
				}
			}
			updateIdle()
		}
	}

	// Signal grab loop if not already stopped.
	s.Stop()

	close(jobs)
	wg.Wait()
	cw.Flush()
	_ = csvFile.Close()

	s.mu.Lock()
	count := s.count
	s.mu.Unlock()
	_ = writeSessionJSON(dir, s.opts, started, &sessionEnd{
		EndedAt: time.Now(),
		Count:   count,
		Reason:  reason.String(),
		Err:     errString(runErr),
	})

	select {
	case <-grabDone:
	case <-time.After(grabJoinWait):
	}

	if e := writerErr.Load(); e != nil && runErr == nil {
		reason, runErr = StopByError, *e
		count = s.count
	}
	return Result{Count: count, Reason: reason, Err: runErr}
}

type sessionEnd struct {
	EndedAt time.Time
	Count   int
	Reason  string
	Err     string
}

func writeSessionJSON(dir string, opts Options, started time.Time, end *sessionEnd) error {
	cfg := opts.Detect
	// Apply same defaults as extract.NewDetector without exporting internals.
	if cfg.AnalyzeWidth <= 0 {
		cfg.AnalyzeWidth = 320
	}
	if cfg.PixelDelta <= 0 {
		cfg.PixelDelta = 25
	}
	if cfg.ChangeRatio <= 0 {
		cfg.ChangeRatio = 0.01
	}
	if cfg.StableDuration <= 0 {
		cfg.StableDuration = 300 * time.Millisecond
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = 200 * time.Millisecond
	}
	if cfg.MinStableFrames <= 0 {
		cfg.MinStableFrames = 3
	}
	if cfg.DupRatio <= 0 {
		cfg.DupRatio = 0.02
	}
	if cfg.MaxShift == 0 {
		cfg.MaxShift = 2
	}

	doc := map[string]any{
		"deviceName":  opts.DeviceName,
		"format":      opts.Format,
		"startedAt":   started.Format(time.RFC3339Nano),
		"idleTimeout": durString(opts.IdleTimeout),
		"detect": map[string]any{
			"analyzeWidth":    cfg.AnalyzeWidth,
			"pixelDelta":      cfg.PixelDelta,
			"changeRatio":     cfg.ChangeRatio,
			"stableDuration":  cfg.StableDuration.String(),
			"lookback":        cfg.Lookback.String(),
			"minStableFrames": cfg.MinStableFrames,
			"dupRatio":        cfg.DupRatio,
			"maxShift":        cfg.MaxShift,
			"roi":             rectString(cfg.ROI),
		},
	}
	if end != nil {
		doc["endedAt"] = end.EndedAt.Format(time.RFC3339Nano)
		doc["count"] = end.Count
		doc["stopReason"] = end.Reason
		if end.Err != "" {
			doc["error"] = end.Err
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "session.json"), append(b, '\n'), 0o644)
}

func durString(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return d.String()
}

func rectString(r image.Rectangle) string {
	if r.Empty() {
		return ""
	}
	return fmt.Sprintf("%d,%d-%d,%d", r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func formatElapsed(d time.Duration) string {
	return formatElapsedFile(d)
}

func formatElapsedFile(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := d.Seconds()
	m := int(sec) / 60
	s := sec - float64(m*60)
	return fmt.Sprintf("t%dm%05.2fs", m, s)
}
