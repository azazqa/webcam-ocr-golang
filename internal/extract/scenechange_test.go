package extract

import (
	"fmt"
	"image"
	"math"
	"math/rand"
	"testing"
	"time"
)

// Simulation: a monitor document shot by a webcam
//   - 640x360, 30fps
//   - sensor noise ±12, slow brightness drift ±12% (auto exposure), occasional bright flicker frames
//   - transitions: cut / 0.5 s fade / 0.3 s slide (screen moves sideways)
//   - mouse cursor moving around / a hand covering the screen for 1 second
//   - page sequence includes a "one line changed" page and a "return to a previous page"

const (
	W, H = 640, 360
	FPS  = 30
)

type page struct {
	name string
	pix  []uint8 // grayscale document image
}

func makePage(name string, seed int64, changeLine int) page {
	rng := rand.New(rand.NewSource(seed))
	p := make([]uint8, W*H)
	for i := range p {
		p[i] = 235 // white background
	}
	for line := 0; line < 14; line++ {
		y0 := 30 + line*22
		r := rng
		if line == changeLine { // only this line gets different content
			r = rand.New(rand.NewSource(seed*1000 + 7))
		}
		x := 40
		for x < W-60 {
			ww := 10 + r.Intn(40)
			for y := y0; y < y0+10; y++ {
				for xx := x; xx < x+ww && xx < W-40; xx++ {
					p[y*W+xx] = 40 // text
				}
			}
			x += ww + 6 + r.Intn(8)
		}
	}
	return page{name, p}
}

type sim struct {
	rng    *rand.Rand
	frames int
}

func (s *sim) render(base []uint8, cursor, hand bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	t := float64(s.frames) / FPS
	gain := 1 + 0.12*math.Sin(t*0.7) // slow auto-exposure drift
	if s.rng.Intn(40) == 0 {
		gain *= 1.15 // occasional flicker
	}
	cx := int(200 + 150*math.Sin(t*2))
	cy := int(150 + 80*math.Cos(t*1.3))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			v := float64(base[y*W+x])
			if cursor && x >= cx && x < cx+12 && y >= cy && y < cy+18 {
				v = 0
			}
			hx := 150 + int(40*math.Sin(t*6)) // a hand is never perfectly still
			if hand && x > hx && x < hx+300 && y > 80 {
				v = 150 // hand
			}
			v = v*gain + float64(s.rng.Intn(25)-12)
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			o := (y*W + x) * 4
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(v), uint8(v), uint8(v), 255
		}
	}
	s.frames++
	return img
}

func blend(a, b []uint8, k float64) []uint8 {
	out := make([]uint8, len(a))
	for i := range a {
		out[i] = uint8(float64(a[i])*(1-k) + float64(b[i])*k)
	}
	return out
}

func slide(a, b []uint8, k float64) []uint8 {
	off := int(k * W)
	out := make([]uint8, len(a))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if x+off < W {
				out[y*W+x] = a[y*W+x+off]
			} else {
				out[y*W+x] = b[y*W+x+off-W]
			}
		}
	}
	return out
}

func TestSceneChangeSimulation(t *testing.T) {
	for _, seed := range []int64{42, 7, 2026, 99, 12345} {
		t.Run(fmt.Sprint("noise_seed_", seed), func(t *testing.T) { runSimulation(t, seed) })
	}
}

func runSimulation(t *testing.T, seed int64) {
	A := makePage("A", 1, -1)
	B := makePage("B", 2, -1)
	C := makePage("C", 3, -1)
	C2 := makePage("C'", 3, 6) // same as C except line 7
	D := makePage("D", 4, -1)

	type step struct {
		pg         page
		hold       int    // display frames
		transition string // transition into this page
		cursor     bool
		hand       bool // hand covers the screen mid-display
	}
	steps := []step{
		{A, 90, "", false, false},
		{B, 120, "cut", true, false},
		{C, 150, "fade", false, true},
		{C2, 120, "cut", false, false}, // one line changed
		{D, 120, "slide", true, false},
		{B, 90, "fade", false, false}, // return to a previous page
	}
	want := []string{"A", "B", "C", "C'", "D", "B"}

	s := &sim{rng: rand.New(rand.NewSource(seed))}
	det := NewDetector(Config{})
	var got []string
	var prev []uint8
	feed := func(base []uint8, cursor, hand bool, name string) {
		ts := time.Duration(s.frames) * time.Second / FPS
		if c, ok := det.Feed(s.render(base, cursor, hand), ts); ok {
			got = append(got, name)
			t.Logf("capture #%d: %-3s t=%6.2fs diff=%.3f", len(got), name, c.Time.Seconds(), c.DiffRatio)
		}
	}
	for _, st := range steps {
		switch st.transition {
		case "fade":
			for i := 1; i <= 15; i++ {
				feed(blend(prev, st.pg.pix, float64(i)/15), false, false, "(fade)")
			}
		case "slide":
			for i := 1; i <= 9; i++ {
				feed(slide(prev, st.pg.pix, float64(i)/9), false, false, "(slide)")
			}
		}
		for i := 0; i < st.hold; i++ {
			hand := st.hand && i > 50 && i < 80
			name := st.pg.name
			if hand {
				name = st.pg.name + "+hand"
			}
			feed(st.pg.pix, st.cursor, hand, name)
		}
		prev = st.pg.pix
	}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("detection result mismatch\n got: %v\nwant: %v", got, want)
	}
}
