//go:build !windows

package integration

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const liveClientTimeout = 4 * time.Minute
const liveClientStartupTimeout = 15 * time.Second

func runLiveClient(t *testing.T, timeout time.Duration, path string, args []string, configure func(*exec.Cmd)) (*exec.Cmd, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
	if configure != nil {
		configure(command)
	}
	runErr := command.Run()
	if command.Process != nil {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			t.Errorf("could not terminate original client process group: %v", err)
		}
	}
	if ctx.Err() != nil {
		return command, ctx.Err()
	}
	return command, runErr
}
