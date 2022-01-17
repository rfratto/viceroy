//go:build !linux && !darwin

package viceroyworkerd

import (
	"fmt"
	"os/exec"
	"runtime"
)

func chroot(*exec.Cmd, string) error {
	return fmt.Errorf("viceroyworkerd chroot unsupported on %s", runtime.GOOS)
}
