package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RunContainerSandbox executes a skill implementation through the local Docker
// CLI. It fails loud when Docker is unavailable; callers record that failure in
// mem_skill_runs instead of pretending the high-risk skill ran.
func RunContainerSandbox(ctx context.Context, cmd []string, opts SandboxOpts) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, errors.New("empty command")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return Result{Stderr: "container sandbox unavailable: docker CLI not found"}, err
	}
	workdir := opts.WorkDir
	if workdir == "" {
		workdir = "."
	}
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		return Result{}, err
	}
	image := strings.TrimSpace(os.Getenv("INFINITY_SKILL_SANDBOX_IMAGE"))
	if image == "" {
		image = "python:3.12-slim"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"run", "--rm",
		"--network", networkMode(opts),
		"--workdir", "/workspace",
		"--volume", absWorkdir + ":/workspace:rw",
		"--pids-limit", "128",
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
	}
	if opts.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", opts.MemoryMB))
	}
	if opts.CPULimit > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(opts.CPULimit, 'f', 2, 64))
	}
	for _, kv := range filterEnv(opts.AllowedEnv) {
		args = append(args, "--env", kv)
	}
	if len(opts.NetworkAllow) > 0 {
		args = append(args, "--env", "SKILL_NETWORK_ALLOW="+strings.Join(opts.NetworkAllow, ","))
	}
	args = append(args, image)
	args = append(args, containerCommand(cmd, absWorkdir)...)

	start := time.Now()
	c := exec.CommandContext(runCtx, "docker", args...)
	out, err := c.CombinedOutput()
	elapsed := time.Since(start)
	res := Result{
		Stdout:     string(out),
		DurationMS: elapsed.Milliseconds(),
		Success:    err == nil,
	}
	if c.ProcessState != nil {
		res.ExitCode = c.ProcessState.ExitCode()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.Stderr = "skill exceeded container sandbox timeout"
		res.Success = false
		return res, runCtx.Err()
	}
	if err != nil {
		res.Stderr = err.Error()
		return res, err
	}
	return res, nil
}

func networkMode(opts SandboxOpts) string {
	if len(opts.NetworkAllow) == 0 {
		return "none"
	}
	// Docker cannot enforce per-domain egress without a proxy. We keep the
	// declared allowlist in SKILL_NETWORK_ALLOW so skill code and future proxy
	// wiring can enforce it, while the container boundary remains explicit.
	return "bridge"
}

func containerCommand(cmd []string, absWorkdir string) []string {
	out := append([]string(nil), cmd...)
	if len(out) < 2 {
		return out
	}
	absImpl, err := filepath.Abs(out[1])
	if err != nil {
		return out
	}
	rel, err := filepath.Rel(absWorkdir, absImpl)
	if err != nil || strings.HasPrefix(rel, "..") {
		return out
	}
	out[1] = filepath.ToSlash(filepath.Join("/workspace", rel))
	return out
}
