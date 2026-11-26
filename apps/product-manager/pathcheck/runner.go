package pathcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const maxOutput = 1 << 20
const maxTemp = 64 << 20

type CommandRunner struct{}

func (CommandRunner) Run(ctx context.Context) (RunResult, error) {
	dir, err := os.MkdirTemp("", "aifence-path-check-")
	if err != nil {
		return RunResult{}, errors.New("cannot create private workspace")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/opt/aifence-playwright/bin/python", "/app/pathcheck/check_purchase_path.py", "--artifact-dir", dir, "--chromium-path", "/usr/bin/chromium")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "PYTHONDONTWRITEBYTECODE=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, errors.New("cannot capture evidence")
	}
	if err = cmd.Start(); err != nil {
		return RunResult{}, errors.New("cannot start browser check")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var total int64
				_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
					if err == nil && info.Mode().IsRegular() {
						total += info.Size()
					}
					return nil
				})
				if total > maxTemp {
					cancel()
					return
				}
			}
		}
	}()
	var out bytes.Buffer
	_, readErr := io.Copy(&out, io.LimitReader(stdout, maxOutput+1))
	if out.Len() > maxOutput {
		cancel()
	}
	waitErr := cmd.Wait()
	ctxErr := ctx.Err()
	cancel()
	<-done
	if ctxErr != nil {
		return RunResult{}, errors.New("time or temporary storage limit exceeded")
	}
	if readErr != nil || waitErr != nil || out.Len() > maxOutput {
		return RunResult{}, errors.New("browser check failed")
	}
	result := RunResult{Evidence: out.Bytes(), Screenshots: make(map[string][]byte)}
	for _, device := range []string{"desktop", "mobile"} {
		path := filepath.Join(dir, device+".png")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return RunResult{}, errors.New("invalid screenshot")
		}
		image, err := os.ReadFile(path)
		if err != nil || len(image) > 8<<20 || !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) {
			return RunResult{}, errors.New("invalid screenshot")
		}
		result.Screenshots[device] = image
	}
	result.Cleanup = func() { _ = os.RemoveAll(dir) }
	keep = true
	return result, nil
}
