//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// hardenCommand puts the child in its own process group and makes
// cancellation kill the whole group, so node, deno or ffmpeg children of
// yt-dlp do not outlive it.
func hardenCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
