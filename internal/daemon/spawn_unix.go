//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// SpawnDetached starts exe with args as a background process in its own
// session, so it outlives the process starting it.
func SpawnDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Executable returns the path of the running binary.
func Executable() (string, error) { return os.Executable() }
