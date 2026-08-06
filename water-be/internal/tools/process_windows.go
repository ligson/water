//go:build windows

package tools

import "os/exec"

func configureCommandProcessGroup(cmd *exec.Cmd) {}

func terminateCommandProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
