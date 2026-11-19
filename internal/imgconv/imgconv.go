// Package imgconv converts raw webcam frames (MJPG/YUY2/NV12/RGB24/RGB32) into image.Image.
// It contains no OS dependencies, so unit tests run on Linux (WSL) too.
package imgconv

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
)

// PixelFormat is a Media Foundation subtype name.
type PixelFormat string

const (
	MJPG  PixelFormat = "MJPG"
	YUY2  PixelFormat = "YUY2"
	NV12  PixelFormat = "NV12"
	RGB24 PixelFormat = "RGB24"
	RGB32 PixelFormat = "RGB32"
)

// Supported reports whether the format can be decoded.
func Supported(f PixelFormat) bool {
	switch f {
	case MJPG, YUY2, NV12, RGB24, RGB32:
		return true
	}
	return false
}

// Decode converts one raw frame into an image.
func Decode(f PixelFormat, data []byte, w, h int) (image.Image, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid resolution %dx%d", w, h)
	}
	switch f {
	case MJPG:
		return jpeg.Decode(bytes.NewReader(FixMJPEG(data)))
	case YUY2:
		return decodeYUY2(data, w, h)
	case NV12:
		return decodeNV12(data, w, h)
	case RGB24:
		return decodeBGR(data, w, h, 3)
	case RGB32:
		return decodeBGR(data, w, h, 4)
	}
	return nil, fmt.Errorf("unsupported pixel format: %s", f)
}

func clamp(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func yuvToRGB(y, u, v uint8) (uint8, uint8, uint8) {
	c := int32(y) - 16
	d := int32(u) - 128
	e := int32(v) - 128
	if c < 0 {
		c = 0
	}
	r := (298*c + 409*e + 128) >> 8
	g := (298*c - 100*d - 208*e + 128) >> 8
	b := (298*c + 516*d + 128) >> 8
	return clamp(r), clamp(g), clamp(b)
}

func decodeYUY2(data []byte, w, h int) (image.Image, error) {
	stride := w * 2
	if len(data) >= h*stride && len(data)/h > stride {
		stride = len(data) / h
	}
	if len(data) < h*stride {
		return nil, fmt.Errorf("YUY2 buffer too short: %d < %d", len(data), h*stride)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := data[y*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x+1 < w; x += 2 {
			i := x * 2
			y0, u, y1, v := src[i], src[i+1], src[i+2], src[i+3]
			r, g, b := yuvToRGB(y0, u, v)
			o := x * 4
			dst[o], dst[o+1], dst[o+2], dst[o+3] = r, g, b, 255
			r, g, b = yuvToRGB(y1, u, v)
			dst[o+4], dst[o+5], dst[o+6], dst[o+7] = r, g, b, 255
		}
	}
	return img, nil
}

func decodeNV12(data []byte, w, h int) (image.Image, error) {
	need := w*h + w*((h+1)/2)
	if len(data) < need {
		return nil, fmt.Errorf("NV12 buffer too short: %d < %d", len(data), need)
	}
	yPlane := data[:w*h]
	uv := data[w*h:]
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		dst := img.Pix[y*img.Stride:]
		uvRow := uv[(y/2)*w:]
		for x := 0; x < w; x++ {
			u := uvRow[(x/2)*2]
			v := uvRow[(x/2)*2+1]
			r, g, b := yuvToRGB(yPlane[y*w+x], u, v)
			o := x * 4
			dst[o], dst[o+1], dst[o+2], dst[o+3] = r, g, b, 255
		}
	}
	return img, nil
}

func decodeBGR(data []byte, w, h, bpp int) (image.Image, error) {
	stride := w * bpp
	if len(data) < h*stride {
		return nil, fmt.Errorf("RGB buffer too short: %d < %d", len(data), h*stride)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := data[y*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			s := x * bpp
			o := x * 4
			dst[o], dst[o+1], dst[o+2], dst[o+3] = src[s+2], src[s+1], src[s], 255
		}
	}
	return img, nil
}

// Downscale shrinks the image so its width is at most maxW (nearest neighbor, for previews).
func Downscale(src image.Image, maxW int) image.Image {
	b := src.Bounds()
	if maxW <= 0 || b.Dx() <= maxW {
		return src
	}
	nw := maxW
	nh := b.Dy() * maxW / b.Dx()
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	rgba, fast := src.(*image.RGBA)
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*b.Dy()/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*b.Dx()/nw
			o := y*dst.Stride + x*4
			if fast {
				i := rgba.PixOffset(sx, sy)
				copy(dst.Pix[o:o+4], rgba.Pix[i:i+4])
			} else {
				r, g, bb, a := src.At(sx, sy).RGBA()
				dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r>>8), uint8(g>>8), uint8(bb>>8), uint8(a>>8)
			}
		}
	}
	return dst
}

// SavePNG saves the image as PNG with fast compression. Creates the directory if needed.
func SavePNG(img image.Image, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
