package skills

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// RunInProcessJail executes cmd in a child process with restricted env and a
// hard timeout. On Linux, applyResourceLimits also installs setrlimit calls
// for CPU time, memory, and file descriptors via sandbox_process_unix.go.
//
// This is the sandbox tier for risk_level=low and risk_level=medium. The
// medium-risk network egress allowlist is enforced by the *caller* — the
// runner wraps any net/http transport with a domain check before invoking
// the skill.
func RunInProcessJail(ctx context.Context, cmd []string, opts SandboxOpts) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, errors.New("empty command")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(runCtx, cmd[0], cmd[1:]...)
	c.Env = filterEnv(opts.AllowedEnv)
	if opts.WorkDir != "" {
		c.Dir = opts.WorkDir
	}
	applyResourceLimits(c, opts)

	start := time.Now()
	stdout, err := c.CombinedOutput()
	elapsed := time.Since(start)

	res := Result{
		Stdout:     string(stdout),
		ExitCode:   c.ProcessState.ExitCode(),
		DurationMS: elapsed.Milliseconds(),
		Success:    err == nil,
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.Stderr = "skill exceeded sandbox timeout"
		res.Success = false
		return res, runCtx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Stderr = string(exitErr.Stderr)
		} else {
			res.Stderr = err.Error()
		}
		return res, err
	}
	return res, nil
}

// filterEnv returns the subset of os.Environ() whose KEY appears in the allow
// list. Skills get a clean environment by default — they explicitly declare
// which env vars they need.
func filterEnv(allowed []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowSet[strings.ToUpper(k)] = true
	}
	all := environ()
	out := all[:0]
	for _, kv := range all {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if allowSet[strings.ToUpper(kv[:eq])] {
			out = append(out, kv)
		}
	}
	return out
}
