package ocr

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// WindowsOCR uses the Windows.Media.Ocr WinRT API through Windows PowerShell 5.1.
// PowerShell 7 (pwsh) cannot call WinRT types directly, so powershell.exe must be used.
type WindowsOCR struct {
	Lang string // BCP-47 tag, e.g. "ko", "en-US"
}

func (w *WindowsOCR) Name() string { return "windows(" + w.Lang + ")" }

// Shared PowerShell prelude: UTF-8 output + a helper to await WinRT async operations.
const psPrelude = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
  $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and
  $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, [Type]$t) {
  $task = $asTaskGeneric.MakeGenericMethod($t).Invoke($null, @($op))
  $task.Wait(-1) | Out-Null
  $task.Result
}
[Windows.Storage.StorageFile, Windows.Storage, ContentType=WindowsRuntime] | Out-Null
[Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
[Windows.Graphics.Imaging.BitmapDecoder, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
[Windows.Globalization.Language, Windows.Foundation, ContentType=WindowsRuntime] | Out-Null
`

const psRecognize = `
$lang = [Windows.Globalization.Language]::new('%s')
if (-not [Windows.Media.Ocr.OcrEngine]::IsLanguageSupported($lang)) {
  [Console]::Error.WriteLine('OCR_LANG_NOT_INSTALLED')
  exit 3
}
$engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)
$file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync('%s')) ([Windows.Storage.StorageFile])
$stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
$decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
$bitmap = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
$max = [Windows.Media.Ocr.OcrEngine]::MaxImageDimension
if ($bitmap.PixelWidth -gt $max -or $bitmap.PixelHeight -gt $max) {
  [Console]::Error.WriteLine("Image too large (max ${max}px)")
  exit 4
}
$result = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
foreach ($line in $result.Lines) { [Console]::Out.WriteLine($line.Text) }
$stream.Dispose()
`

const psListLangs = `
foreach ($l in [Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages) {
  [Console]::Out.WriteLine($l.LanguageTag + [char]9 + $l.DisplayName)
}
`

// psQuote escapes a string for a PowerShell single-quoted literal.
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

// encodePS encodes a script as UTF-16LE Base64 for -EncodedCommand (no quoting problems).
func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[i*2] = byte(c)
		b[i*2+1] = byte(c >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runPS(ctx context.Context, script string) (string, string, error) {
	return runCmd(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePS(script))
}

// BuildRecognizeScript returns the full PowerShell script (for tests and debugging).
func (w *WindowsOCR) BuildRecognizeScript(absPath string) string {
	return psPrelude + fmt.Sprintf(psRecognize, psQuote(w.Lang), psQuote(absPath))
}

func (w *WindowsOCR) Recognize(ctx context.Context, imagePath string) (string, error) {
	abs, err := filepath.Abs(imagePath)
	if err != nil {
		return "", err
	}
	out, stderr, err := runPS(ctx, w.BuildRecognizeScript(abs))
	if err != nil {
		if strings.Contains(stderr, "OCR_LANG_NOT_INSTALLED") {
			return "", fmt.Errorf("the Windows OCR language pack for '%s' is not installed. "+
				"Install it under Settings > Time & language > Language & region > add language > Optical character recognition", w.Lang)
		}
		return "", fmt.Errorf("Windows OCR failed: %v\n%s", err, normalize(stderr))
	}
	return normalize(out), nil
}

// ListWindowsOCRLanguages returns the OCR languages installed on this PC as "tag\tname" lines.
func ListWindowsOCRLanguages(ctx context.Context) ([]string, error) {
	out, stderr, err := runPS(ctx, psPrelude+psListLangs)
	if err != nil {
		return nil, fmt.Errorf("language list query failed: %v\n%s", err, normalize(stderr))
	}
	var langs []string
	for _, l := range strings.Split(normalize(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			langs = append(langs, l)
		}
	}
	return langs, nil
}
