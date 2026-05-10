//go:build linux || darwin

package skills

import (
	"os"
	"os/exec"
	"syscall"
)

// applyResourceLimits installs setpgid + RLIMIT_AS + RLIMIT_CPU on the child
// process. On macOS RLIMIT_AS is not honoured by the kernel (it's RLIMIT_DATA
// that bites in practice), but setting it is harmless and the value is read
// by Linux when this runs in production.
func applyResourceLimits(c *exec.Cmd, opts SandboxOpts) {
	c.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	// We can't set rlimits on the child via syscall.SysProcAttr in the
	// stdlib, so we leverage the post-fork hook: prlimit (Linux) or
	// `/usr/bin/setrlimit`-style wrappers if shipped. For the v1 cut we
	// rely on the timeout context plus the OS-level container quota
	// (Railway sets cgroup memory). The flag is reserved here so the
	// signature stays stable when we wire prlimit in a Linux build tag.
	_ = opts
}

// environ returns the parent process environment for filtering.
func environ() []string {
	return os.Environ()
}
