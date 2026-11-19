//go:build windows

package ocr

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideWindow keeps a console window from flashing up when a child process starts from the GUI exe.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
