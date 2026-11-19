// Package extract detects screen transitions in a webcam video and picks out one image per screen.
//
// How it works (pure Go, no external libraries):
//  1. Shrink each frame to a small grayscale image (default width 320)
//  2. Normalize brightness (scale the mean to 128)
//  3. Changed-pixel ratio vs previous frame and vs Lookback ago (~0.2 s)
//  4. Stable when below ChangeRatio for StableDuration and at least MinStableFrames
//  5. Pick the sharpest frame from the stable window
//  6. Skip duplicates vs last saved (DupRatio, ±MaxShift)
package extract

import (
	"image"
	"time"
)

// Config holds the detection parameters.
type Config struct {
	AnalyzeWidth   int             // analysis image width (px). Default 320
	ROI            image.Rectangle // region to watch. Empty = whole frame
	PixelDelta     int             // per-pixel difference threshold (0~255). Default 25
	ChangeRatio    float64         // changed-pixel ratio that counts as "changing". Default 0.01
	StableDuration time.Duration   // time the screen must stay stable. Default 300ms
	Lookback       time.Duration   // compare with a frame this far back. Default 200ms
	MinStableFrames int            // minimum stable frames (with StableDuration). Default 3
	DupRatio       float64         // below this vs last saved = same screen. Default 0.02
	MaxShift       int             // position tolerance for duplicate check. Default 2
	MinInterval    time.Duration   // minimum gap between saves. Default 0
}

func (c *Config) defaults() {
	if c.AnalyzeWidth <= 0 {
		c.AnalyzeWidth = 320
	}
	if c.PixelDelta <= 0 {
		c.PixelDelta = 25
	}
	if c.ChangeRatio <= 0 {
		c.ChangeRatio = 0.01
	}
	if c.StableDuration <= 0 {
		c.StableDuration = 300 * time.Millisecond
	}
	if c.Lookback <= 0 {
		c.Lookback = 200 * time.Millisecond
	}
	if c.MinStableFrames <= 0 {
		c.MinStableFrames = 3
	}
	if c.DupRatio <= 0 {
		c.DupRatio = 0.02
	}
	if c.MaxShift < 0 {
		c.MaxShift = 0
	} else if c.MaxShift == 0 {
		c.MaxShift = 2
	}
}

// Capture is one detected screen.
type Capture struct {
	Image     image.Image
	Time      time.Duration
	DiffRatio float64
}

type histEntry struct {
	gray []uint8
	t    time.Duration
}

// Detector receives frames in order and returns a Capture whenever a new screen settles.
type Detector struct {
	cfg Config

	hist        []histEntry
	stableCount int
	stableSince time.Duration
	lastChange  time.Duration
	started     bool
	emitted     bool
	lastSaved   []uint8
	lastSavedAt time.Duration
	hasSaved    bool

	best      image.Image
	bestGray  []uint8
	bestSharp float64
	bestTime  time.Duration

	w, h int
}

// NewDetector creates a detector.
func NewDetector(cfg Config) *Detector {
	cfg.defaults()
	return &Detector{cfg: cfg}
}

// Feed takes one frame and its session-relative time. Returns a Capture when a new screen settles.
func (d *Detector) Feed(img image.Image, t time.Duration) (Capture, bool) {
	g := d.analyze(img)
	if !d.started {
		d.started = true
		d.hist = []histEntry{{g, t}}
		d.lastChange = t
		d.stableSince = t
		d.resetWindow()
		d.consider(img, g, t)
		return Capture{}, false
	}

	prev := d.hist[len(d.hist)-1].gray
	back := d.lookbackGray(t)
	rPrev := diffRatio(g, prev, d.cfg.PixelDelta)
	rBack := diffRatio(g, back, d.cfg.PixelDelta)

	d.hist = append(d.hist, histEntry{g, t})
	d.trimHist(t)

	if rPrev > d.cfg.ChangeRatio || rBack > d.cfg.ChangeRatio {
		d.lastChange = t
		d.stableCount = 0
		d.stableSince = t
		d.emitted = false
		d.resetWindow()
		return Capture{}, false
	}

	if d.stableCount == 0 {
		d.stableSince = t
	}
	d.stableCount++
	d.consider(img, g, t)
	if d.emitted {
		return Capture{}, false
	}
	if d.stableCount < d.cfg.MinStableFrames || t-d.stableSince < d.cfg.StableDuration {
		return Capture{}, false
	}
	d.emitted = true

	dr := 1.0
	if d.hasSaved {
		dr = diffRatioShift(d.bestGray, d.lastSaved, d.w, d.h, d.cfg.PixelDelta, d.cfg.MaxShift)
		if dr < d.cfg.DupRatio {
			return Capture{}, false
		}
		if d.cfg.MinInterval > 0 && d.bestTime-d.lastSavedAt < d.cfg.MinInterval {
			return Capture{}, false
		}
	}
	d.lastSaved, d.lastSavedAt, d.hasSaved = d.bestGray, d.bestTime, true
	return Capture{Image: d.best, Time: d.bestTime, DiffRatio: dr}, true
}

// MarkSaved records img as the last saved screen for duplicate detection.
// Does not update LastChange. Returns the diff ratio vs the previous last-saved (1.0 if none).
func (d *Detector) MarkSaved(img image.Image, t time.Duration) float64 {
	g := d.analyze(img)
	dr := 1.0
	if d.hasSaved {
		dr = diffRatioShift(g, d.lastSaved, d.w, d.h, d.cfg.PixelDelta, d.cfg.MaxShift)
	}
	d.lastSaved, d.lastSavedAt, d.hasSaved = g, t, true
	return dr
}

// LastChange returns the session time when the screen last entered a "changing" state.
func (d *Detector) LastChange() time.Duration { return d.lastChange }

// HasSaved reports whether at least one screen has been marked saved.
func (d *Detector) HasSaved() bool { return d.hasSaved }

func (d *Detector) lookbackGray(t time.Duration) []uint8 {
	target := t - d.cfg.Lookback
	best := d.hist[0]
	found := false
	for _, e := range d.hist {
		if e.t <= target {
			best = e
			found = true
		}
	}
	if found {
		return best.gray
	}
	return d.hist[0].gray
}

func (d *Detector) trimHist(t time.Duration) {
	// Keep frames newer than Lookback*2 plus a few older for lookback selection.
	keepFrom := t - 2*d.cfg.Lookback - 50*time.Millisecond
	i := 0
	for i < len(d.hist)-1 && d.hist[i].t < keepFrom {
		i++
	}
	if i > 0 {
		d.hist = append([]histEntry(nil), d.hist[i:]...)
	}
}

func (d *Detector) resetWindow() {
	d.best, d.bestGray, d.bestSharp = nil, nil, -1
}

func (d *Detector) consider(img image.Image, g []uint8, t time.Duration) {
	s := sharpness(g, d.w, d.h)
	if s > d.bestSharp {
		d.best, d.bestGray, d.bestSharp, d.bestTime = img, g, s, t
	}
}

func (d *Detector) analyze(img image.Image) []uint8 {
	b := img.Bounds()
	if !d.cfg.ROI.Empty() {
		b = d.cfg.ROI.Intersect(b)
	}
	w := d.cfg.AnalyzeWidth
	if w > b.Dx() {
		w = b.Dx()
	}
	h := b.Dy() * w / b.Dx()
	if h < 1 {
		h = 1
	}
	d.w, d.h = w, h

	sum := make([]uint32, w*h)
	cnt := make([]uint32, w*h)
	rgba, fast := img.(*image.RGBA)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		cy := (y - b.Min.Y) * h / b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			cx := (x - b.Min.X) * w / b.Dx()
			var lum uint32
			if fast {
				i := rgba.PixOffset(x, y)
				p := rgba.Pix[i : i+3]
				lum = (299*uint32(p[0]) + 587*uint32(p[1]) + 114*uint32(p[2])) / 1000
			} else {
				r, gg, bb, _ := img.At(x, y).RGBA()
				lum = (299*(r>>8) + 587*(gg>>8) + 114*(bb>>8)) / 1000
			}
			sum[cy*w+cx] += lum
			cnt[cy*w+cx]++
		}
	}
	out := make([]uint8, w*h)
	var total uint64
	for i := range out {
		if cnt[i] > 0 {
			out[i] = uint8(sum[i] / cnt[i])
		}
		total += uint64(out[i])
	}
	mean := float64(total) / float64(len(out))
	if mean > 1 {
		k := 128 / mean
		for i, v := range out {
			n := float64(v) * k
			if n > 255 {
				n = 255
			}
			out[i] = uint8(n)
		}
	}
	return out
}

func diffRatio(a, b []uint8, delta int) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 1
	}
	n := 0
	for i := range a {
		dv := int(a[i]) - int(b[i])
		if dv < 0 {
			dv = -dv
		}
		if dv > delta {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

func diffRatioShift(a, b []uint8, w, h, delta, maxShift int) float64 {
	if maxShift <= 0 || w <= 2*maxShift || h <= 2*maxShift {
		return diffRatio(a, b, delta)
	}
	if len(a) != w*h || len(b) != w*h {
		return 1
	}
	best := 1.0
	for dy := -maxShift; dy <= maxShift; dy++ {
		for dx := -maxShift; dx <= maxShift; dx++ {
			n, c := 0, 0
			for y := maxShift; y < h-maxShift; y++ {
				ia := y * w
				ib := (y+dy)*w + dx
				for x := maxShift; x < w-maxShift; x++ {
					dv := int(a[ia+x]) - int(b[ib+x])
					if dv < 0 {
						dv = -dv
					}
					if dv > delta {
						n++
					}
					c++
				}
			}
			if r := float64(n) / float64(c); r < best {
				best = r
			}
		}
	}
	return best
}

func sharpness(g []uint8, w, h int) float64 {
	if w < 2 || h < 2 {
		return 0
	}
	var s int64
	for y := 0; y < h-1; y++ {
		for x := 0; x < w-1; x++ {
			i := y*w + x
			dx := int64(g[i+1]) - int64(g[i])
			dy := int64(g[i+w]) - int64(g[i])
			if dx < 0 {
				dx = -dx
			}
			if dy < 0 {
				dy = -dy
			}
			s += dx + dy
		}
	}
	return float64(s) / float64((w-1)*(h-1))
}
