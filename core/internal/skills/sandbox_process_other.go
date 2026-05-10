//go:build !(linux || darwin)

package skills

import (
	"os"
	"os/exec"
)

func applyResourceLimits(c *exec.Cmd, opts SandboxOpts) { _ = c; _ = opts }

func environ() []string { return os.Environ() }
