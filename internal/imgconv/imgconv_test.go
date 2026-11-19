package imgconv

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestDHTTableSizes(t *testing.T) {
	check := func(name string, bits [16]byte, vals []byte) {
		sum := 0
		for _, b := range bits {
			sum += int(b)
		}
		if sum != len(vals) {
			t.Errorf("%s: bits sum %d != vals %d", name, sum, len(vals))
		}
	}
	check("dcLum", dcLumBits, dcLumVals)
	check("dcChrom", dcChromBits, dcChromVals)
	check("acLum", acLumBits, acLumVals)
	check("acChrom", acChromBits, acChromVals)
	if len(stdDHT) != 420 { // 4 marker/length bytes + 416 body bytes
		t.Errorf("stdDHT length %d, want 420", len(stdDHT))
	}
}

// stripDHT removes every DHT segment, simulating a webcam MJPEG frame.
func stripDHT(b []byte) []byte {
	out := []byte{b[0], b[1]}
	i := 2
	for i+4 <= len(b) {
		m := b[i+1]
		if m == 0xDA {
			return append(out, b[i:]...)
		}
		l := int(b[i+2])<<8 | int(b[i+3])
		if m != 0xC4 {
			out = append(out, b[i:i+2+l]...)
		}
		i += 2 + l
	}
	return out
}

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), 128, 255})
		}
	}
	return img
}

func TestFixMJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	orig := buf.Bytes()
	if !hasDHT(orig) {
		t.Fatal("encoded JPEG should contain DHT")
	}
	noDHT := stripDHT(orig)
	if hasDHT(noDHT) {
		t.Fatal("DHT strip failed")
	}
	if _, err := jpeg.Decode(bytes.NewReader(noDHT)); err == nil {
		t.Fatal("decoding without DHT should fail")
	}
	img, err := Decode(MJPG, noDHT, 64, 48)
	if err != nil {
		t.Fatalf("decode after inserting DHT failed: %v", err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
		t.Fatalf("size mismatch: %v", img.Bounds())
	}
	if got := FixMJPEG(orig); !bytes.Equal(got, orig) {
		t.Fatal("a JPEG that already has DHT must not be modified")
	}
}

func TestYUY2(t *testing.T) {
	// 2x1 pixels: white (Y=235) and black (Y=16), U=V=128
	data := []byte{235, 128, 16, 128}
	img, err := Decode(YUY2, data, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := img.At(0, 0).RGBA()
	r2, _, _, _ := img.At(1, 0).RGBA()
	if r>>8 != 255 || r2>>8 != 0 {
		t.Fatalf("YUY2 conversion error: %d %d", r>>8, r2>>8)
	}
}

func TestNV12(t *testing.T) {
	w, h := 4, 2
	data := make([]byte, w*h+w*h/2)
	for i := 0; i < w*h; i++ {
		data[i] = 235
	}
	for i := w * h; i < len(data); i++ {
		data[i] = 128
	}
	img, err := Decode(NV12, data, w, h)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(3, 1).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Fatalf("NV12 conversion error: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestRGB32AndDownscale(t *testing.T) {
	data := []byte{10, 20, 30, 0, 40, 50, 60, 0} // BGRX x2
	img, err := Decode(RGB32, data, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 30 || g>>8 != 20 || b>>8 != 10 {
		t.Fatalf("BGR order error: %d %d %d", r>>8, g>>8, b>>8)
	}
	small := Downscale(testImage(), 32)
	if small.Bounds().Dx() != 32 || small.Bounds().Dy() != 24 {
		t.Fatalf("downscale error: %v", small.Bounds())
	}
}
