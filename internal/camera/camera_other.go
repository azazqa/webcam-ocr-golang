//go:build !windows

package camera

import (
	"errors"
	"image"
)

var errUnsupported = errors.New("camera capture is only supported on Windows")

func ListDevices() ([]Device, error) { return nil, errUnsupported }

type Camera struct{ Info Device }

func Open(d Device) (*Camera, error)           { return nil, errUnsupported }
func (c *Camera) Formats() []Format            { return nil }
func (c *Camera) Current() Format              { return Format{} }
func (c *Camera) Capturing() bool              { return false }
func (c *Camera) Start(f Format) error         { return errUnsupported }
func (c *Camera) EnsureStart(f Format) error   { return errUnsupported }
func (c *Camera) Grab() (image.Image, error)   { return nil, errUnsupported }
func (c *Camera) Warmup(n int)                 {}
func (c *Camera) Close()                       {}
