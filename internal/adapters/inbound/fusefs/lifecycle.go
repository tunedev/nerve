package fusefs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// helper is the setuid program that mounts and unmounts for an unprivileged
// process. It is the same one go-fuse calls, looked up the same way, because a
// mount this package made and a mount it is reclaiming are the same kind of
// mount.
func helper() (string, error) {
	for _, spelling := range []string{"fusermount3", "fusermount"} {
		if path, err := exec.LookPath(spelling); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no fusermount3 or fusermount on PATH")
}

// detach takes the mount at dir out of the mount table without waiting for
// what is holding it. The kernel frees it when the last descriptor opened
// through it closes; until then those descriptors keep answering, and no path
// reaches the mount any more.
func detach(dir string) error {
	bin, err := helper()
	if err != nil {
		return err
	}

	out, err := exec.Command(bin, "-u", "-z", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s -u -z %s: %w: %s", bin, dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// reclaim clears a mount left at dir by a process that died holding one.
//
// A FUSE mount outlives the process serving it. The kernel keeps the mount
// entry, and every path through it answers ENOTCONN, so the point stays
// occupied and unusable until something unmounts it: SIGKILL and a panic both
// end the process with no code left to run, and nothing else can clear it.
//
// ENOTCONN is the one condition worth acting on. A live mount answers, and a
// mount whose server is wedged rather than gone blocks instead of failing, so
// this reclaims exactly the mounts that have no server left.
func reclaim(dir string) error {
	if _, err := os.Stat(dir); !errors.Is(err, syscall.ENOTCONN) {
		return nil
	}
	if err := detach(dir); err != nil {
		return fmt.Errorf("reclaim %q: %w", dir, err)
	}
	return nil
}
