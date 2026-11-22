//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the generator in its own process group so a timeout
// can stop the whole tree.
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
