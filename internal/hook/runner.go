package hook

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// TrustedRunner runs the ldconfig shipped with module-hook, never the
// image's, as "ldconfig -X -r <rootfs>". ldconfig chroots into the rootfs
// itself, so it reads the image's ld.so.conf and writes the image's
// ld.so.cache; -X leaves the image's library symlinks alone.
type TrustedRunner struct {
	Timeout time.Duration
}

func (r TrustedRunner) Run(rootfs, ldconfig string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ldconfig, "-X", "-r", rootfs)
	cmd.WaitDelay = time.Second // don't wait on pipes held by killed children
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", ldconfig, err, out)
	}
	return nil
}
