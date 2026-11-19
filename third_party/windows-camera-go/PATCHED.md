# Local patch

Upstream: https://github.com/Kirizu-Official/windows-camera-go (v0.2.0 + main, MIT)

v0.2.0 still had `import "C"` in two files (`windows/mf/IMFMediaBuffer.go`,
`windows/mf/mf.go`), so the `IMFMediaBuffer` type was dropped when building
with `CGO_ENABLED=0`, and the build failed.

Changes:
- Removed `import "C"` from both files
- Replaced `C.GoBytes(...)` with `make` + `copy(unsafe.Slice(...))`
- Removed `examples/`
- `camera/v1/device.go` `CloseDevice()`: added `IMFMediaSource.Shutdown()` so the camera is
  released when closed (LED off), and nil out each pointer after Release to prevent double Release
