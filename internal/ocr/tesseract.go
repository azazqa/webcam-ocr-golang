package ocr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Tesseract calls the Tesseract CLI (https://github.com/UB-Mannheim/tesseract/wiki).
type Tesseract struct {
	Lang string // e.g. "kor+eng"
	Path string // empty: auto-detect via PATH or the default install location
	PSM  int    // page segmentation mode, 0 = default(3)
}

func (t *Tesseract) Name() string { return "tesseract(" + t.Lang + ")" }

func (t *Tesseract) bin() (string, error) {
	if t.Path != "" {
		return t.Path, nil
	}
	if p, err := exec.LookPath("tesseract"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, p := range []string{
			`C:\Program Files\Tesseract-OCR\tesseract.exe`,
			`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("tesseract executable not found. Install it, then add it to PATH or pass -tesseract <path>")
}

func (t *Tesseract) Recognize(ctx context.Context, imagePath string) (string, error) {
	bin, err := t.bin()
	if err != nil {
		return "", err
	}
	psm := t.PSM
	if psm == 0 {
		psm = 3
	}
	out, stderr, err := runCmd(ctx, bin, imagePath, "stdout", "-l", t.Lang, "--psm", fmt.Sprint(psm))
	if err != nil {
		return "", fmt.Errorf("tesseract failed: %v\n%s", err, normalize(stderr))
	}
	return normalize(out), nil
}
