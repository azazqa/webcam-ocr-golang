//go:build windows

package camera

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"

	wc "github.com/Kirizu-Official/windows-camera-go/camera/v1"
	"github.com/Kirizu-Official/windows-camera-go/windows/guid"

	"webcam-ocr/internal/imgconv"
)

// COM/MF calls run on one LockOSThread worker to avoid clashing with a GUI STA thread.
var (
	workerOnce sync.Once
	workerErr  error
	jobs       = make(chan func())
)

func startWorker() {
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		workerErr = wc.Init()
		close(ready)
		if workerErr != nil {
			return
		}
		for j := range jobs {
			j()
		}
	}()
	<-ready
}

func call(f func() error) (err error) {
	workerOnce.Do(startWorker)
	if workerErr != nil {
		return fmt.Errorf("Media Foundation initialization failed: %w", workerErr)
	}
	done := make(chan error, 1)
	jobs <- func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("camera internal error: %v", r)
			}
		}()
		done <- f()
	}
	return <-done
}

// ListDevices returns the connected video capture devices.
func ListDevices() ([]Device, error) {
	var out []Device
	err := call(func() error {
		list, err := wc.EnumDevice()
		if err != nil {
			return err
		}
		for i, d := range list {
			out = append(out, Device{Index: i, Name: d.Name, ID: d.SymbolLink})
		}
		return nil
	})
	return out, err
}

// Camera is an open camera.
type Camera struct {
	Info    Device
	dev     *wc.Device
	cap     *wc.CaptureSync
	format  Format
	formats []Format
	mu      sync.Mutex
}

var pixelGUIDs = []struct {
	g *guid.GUID
	p imgconv.PixelFormat
}{
	{&guid.SubTypeMediaSubTypeMJPG, imgconv.MJPG},
	{&guid.SubTypeMediaSubTypeYUY2, imgconv.YUY2},
	{&guid.SubTypeMediaSubTypeNV12, imgconv.NV12},
	{&guid.SubTypeMediaSubTypeRGB24, imgconv.RGB24},
	{&guid.SubTypeMediaSubTypeRGB32, imgconv.RGB32},
}

func pixelOf(g *guid.GUID) (imgconv.PixelFormat, bool) {
	if g == nil {
		return "", false
	}
	for _, x := range pixelGUIDs {
		if g.IsMatch(x.g) {
			return x.p, true
		}
	}
	return "", false
}

// Open opens a camera and reads its list of supported formats.
func Open(d Device) (*Camera, error) {
	c := &Camera{Info: d}
	err := call(func() error {
		dev, err := wc.OpenDevice(d.ID)
		if err != nil {
			return err
		}
		c.dev = dev
		fs, err := dev.EnumerateCaptureFormats()
		if err != nil {
			return err
		}
		for _, f := range fs {
			p, ok := pixelOf(f.SubType)
			if !ok {
				continue
			}
			c.formats = append(c.formats, Format{
				Index: len(c.formats), Pixel: p,
				Width: int(f.Width), Height: int(f.Height), FPS: f.Fps, raw: f,
			})
		}
		return nil
	})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("failed to open camera '%s': %w", d.Name, err)
	}
	if len(c.formats) == 0 {
		c.Close()
		return nil, errors.New("this camera has no supported format (MJPG/YUY2/NV12/RGB)")
	}
	return c, nil
}

func (c *Camera) Formats() []Format { return c.formats }

func (c *Camera) Current() Format {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.format
}

// Start begins capturing in the given format.
func (c *Camera) Start(f Format) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, ok := f.raw.(*wc.CaptureFormats)
	if !ok {
		return errors.New("invalid format")
	}
	if c.cap != nil {
		return errors.New("capture already started (Close and reopen to change format)")
	}
	return call(func() error {
		cs, err := c.dev.StartCapture(raw)
		if err != nil {
			return err
		}
		c.cap = cs
		c.format = f
		return nil
	})
}

// Grab reads one frame and converts it to an image.
// The camera mutex is not held while waiting for GetFrame, so Close is not
// blocked by a long exclusive lock on c.mu (worker-queue still serializes MF calls).
func (c *Camera) Grab() (image.Image, error) {
	c.mu.Lock()
	cap := c.cap
	dev := c.dev
	format := c.format
	c.mu.Unlock()
	if cap == nil || dev == nil {
		return nil, errors.New("capture has not started")
	}

	var data []byte
	err := call(func() error {
		c.mu.Lock()
		still := c.cap == cap && c.dev == dev
		c.mu.Unlock()
		if !still {
			return errors.New("camera closed")
		}
		frame, err := cap.GetFrame()
		if err != nil {
			return err
		}
		defer frame.Release()
		buf, err := dev.ParseSampleToBuffer(frame.PpSample)
		if err != nil {
			return err
		}
		defer buf.Release()
		n := int(buf.Length)
		if n > len(buf.Buffer) {
			n = len(buf.Buffer)
		}
		data = append([]byte(nil), buf.Buffer[:n]...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return imgconv.Decode(format.Pixel, data, format.Width, format.Height)
}

// Warmup discards the first n frames so auto exposure / white balance can settle.
func (c *Camera) Warmup(n int) {
	for i := 0; i < n; i++ {
		if _, err := c.Grab(); err != nil {
			return
		}
	}
}

// Close releases the camera. Prefer stopping capture sessions before calling Close
// so an in-flight GetFrame on the MF worker is less likely to block indefinitely.
func (c *Camera) Close() {
	c.mu.Lock()
	dev := c.dev
	c.dev, c.cap = nil, nil
	c.mu.Unlock()
	if dev == nil {
		return
	}
	_ = call(func() error {
		dev.CloseDevice()
		return nil
	})
}
