//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createBreakawayJob    = 0x01000000
)

// SpawnDetached starts exe with args as a background process that outlives the
// process starting it, has no console, and is not part of the caller's job
// (clients often run their servers in a job that is killed with them).
func SpawnDetached(exe string, args ...string) error {
	start := func(flags uint32) error {
		cmd := exec.Command(exe, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	// Breaking away from the job is refused when the job doesn't allow it;
	// then the daemon starts inside it, as the best available.
	if err := start(detachedProcess | createNewProcessGroup | createBreakawayJob); err == nil {
		return nil
	}
	return start(detachedProcess | createNewProcessGroup)
}

// Executable returns the path of the running binary.
func Executable() (string, error) { return os.Executable() }
