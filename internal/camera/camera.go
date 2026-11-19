// Package camera captures frames from a USB webcam on Windows through Media Foundation.
// No CGO; cross-compiles from WSL with GOOS=windows.
package camera

import (
	"fmt"
	"sort"

	"webcam-ocr/internal/imgconv"
)

// Device is a connected camera.
type Device struct {
	Index int
	Name  string
	ID    string // Media Foundation symbolic link
}

// Format is a capture format (resolution / pixel format / FPS) the camera supports.
type Format struct {
	Index  int
	Pixel  imgconv.PixelFormat
	Width  int
	Height int
	FPS    float64
	raw    any // *camera.CaptureFormats (windows only)
}

func (f Format) String() string {
	return fmt.Sprintf("%dx%d %s %.0ffps", f.Width, f.Height, f.Pixel, f.FPS)
}

// SameFormat reports whether two formats describe the same capture mode.
func (f Format) SameFormat(o Format) bool {
	return f.Width == o.Width && f.Height == o.Height && f.Pixel == o.Pixel && f.FPS == o.FPS
}

// BestFormat picks the format best suited for OCR: highest resolution first,
// then MJPG at the same resolution (higher fps, less USB bandwidth), then higher fps.
func BestFormat(fs []Format) (Format, bool) {
	if len(fs) == 0 {
		return Format{}, false
	}
	c := append([]Format(nil), fs...)
	sort.SliceStable(c, func(i, j int) bool {
		ai, aj := c[i].Width*c[i].Height, c[j].Width*c[j].Height
		if ai != aj {
			return ai > aj
		}
		if (c[i].Pixel == imgconv.MJPG) != (c[j].Pixel == imgconv.MJPG) {
			return c[i].Pixel == imgconv.MJPG
		}
		return c[i].FPS > c[j].FPS
	})
	return c[0], true
}
