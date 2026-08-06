//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCommandProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateCommandProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr != nil && killErr != syscall.ESRCH {
			return killErr
		}
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errorsIsProcessDone(err) {
		return err
	}
	return nil
}

func errorsIsProcessDone(err error) bool {
	return errors.Is(err, os.ErrProcessDone)
}
