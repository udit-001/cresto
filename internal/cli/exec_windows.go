//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess,
	}
}
