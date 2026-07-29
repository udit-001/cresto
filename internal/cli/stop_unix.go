//go:build !windows

package cli

import (
	"fmt"
	"os"
	"syscall"
)

func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("no process with PID %d found", pid)
	}
	_ = proc.Signal(syscall.SIGINT)
	return nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
