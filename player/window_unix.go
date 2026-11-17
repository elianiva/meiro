//go:build !windows

package player

import "os/exec"

func hideWindow(*exec.Cmd) {}
