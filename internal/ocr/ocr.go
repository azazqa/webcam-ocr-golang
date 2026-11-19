// Package ocr extracts text from an image file.
//
// Two engines are provided:
//   - windows   : Windows 10/11 built-in OCR (Windows.Media.Ocr). Nothing to install.
//     Called through Windows PowerShell 5.1 (powershell.exe).
//   - tesseract : Tesseract OCR CLI (requires the UB Mannheim build + kor.traineddata).
package ocr

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Engine is the common OCR engine interface.
type Engine interface {
	Name() string
	// Recognize extracts text from the image file at imagePath.
	Recognize(ctx context.Context, imagePath string) (string, error)
}

// New creates an engine by name. lang: e.g. windows="ko"/"en-US", tesseract="kor+eng".
func New(name, lang, tesseractPath string) (Engine, error) {
	switch strings.ToLower(name) {
	case "", "windows", "win":
		if lang == "" {
			lang = "ko"
		}
		return &WindowsOCR{Lang: lang}, nil
	case "tesseract", "tess":
		if lang == "" {
			lang = "kor+eng"
		}
		return &Tesseract{Lang: lang, Path: tesseractPath}, nil
	}
	return nil, fmt.Errorf("unknown OCR engine: %s (windows | tesseract)", name)
}

func runCmd(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimPrefix(s, "\ufeff")
	return strings.TrimSpace(s)
}
