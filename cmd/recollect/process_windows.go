//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

func configureProcess(command *exec.Cmd) {}

func killProcessTree(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
}
