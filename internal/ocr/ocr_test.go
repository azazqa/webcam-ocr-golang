package ocr

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEncodePSRoundTrip(t *testing.T) {
	src := "Write-Output '한글 테스트'"
	raw, err := base64.StdEncoding.DecodeString(encodePS(src))
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	if got := string(utf16.Decode(u)); got != src {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestRecognizeScriptQuoting(t *testing.T) {
	w := &WindowsOCR{Lang: "ko"}
	s := w.BuildRecognizeScript(`C:\Users\홍길동\It's\cap.png`)
	if !strings.Contains(s, `GetFileFromPathAsync('C:\Users\홍길동\It''s\cap.png')`) {
		t.Fatal("path escaping error")
	}
	if !strings.Contains(s, "IAsyncOperation`1") {
		t.Fatal("generic type name (backtick) missing")
	}
	if strings.Count(s, "%") > 0 && strings.Contains(s, "%!") {
		t.Fatal("format string error")
	}
}

func TestNew(t *testing.T) {
	e, err := New("tesseract", "", "")
	if err != nil || e.Name() != "tesseract(kor+eng)" {
		t.Fatalf("%v %v", e, err)
	}
	if _, err := New("foo", "", ""); err == nil {
		t.Fatal("unknown engine should return an error")
	}
}
