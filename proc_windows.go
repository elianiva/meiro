//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// hardenCommand makes cancellation kill the child's whole process tree.
func hardenCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
