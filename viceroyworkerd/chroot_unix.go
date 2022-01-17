//go:build linux || darwin

package viceroyworkerd

import (
	"os/exec"
	"syscall"
)

func chroot(cmd *exec.Cmd, dir string) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Chroot = dir
	return nil
}
