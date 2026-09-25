package hook

import (
	"fmt"
	"os/exec"
	"syscall"
)

// ChrootRunner runs ldconfig in a child process chrooted into the rootfs,
// so the image's own ldconfig builds the image's own cache.
type ChrootRunner struct{}

func (ChrootRunner) Run(rootfs, ldconfig string) error {
	cmd := exec.Command(ldconfig)
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: rootfs}
	cmd.Dir = "/"
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", ldconfig, err, out)
	}
	return nil
}
