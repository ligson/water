package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/creack/pty"
)

type LocalShell struct {
	cmd *exec.Cmd
	pty *os.File
}

func StartLocalShell(ctx context.Context, cwd string, cols, rows int) (*LocalShell, error) {
	name, args := interactiveShellCommand()
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}

	size := &pty.Winsize{
		Cols: uint16(defaultShellDimension(cols, 100)),
		Rows: uint16(defaultShellDimension(rows, 30)),
	}
	file, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, fmt.Errorf("start local shell: %w", err)
	}
	return &LocalShell{cmd: cmd, pty: file}, nil
}

func (s *LocalShell) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

func (s *LocalShell) Write(p []byte) (int, error) {
	return s.pty.Write(p)
}

func (s *LocalShell) Close() error {
	if s.pty != nil {
		_ = s.pty.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (s *LocalShell) Wait() error {
	if s.cmd == nil {
		return nil
	}
	return s.cmd.Wait()
}

func (s *LocalShell) Resize(cols, rows int) error {
	if s.pty == nil {
		return nil
	}
	return pty.Setsize(s.pty, &pty.Winsize{
		Cols: uint16(defaultShellDimension(cols, 100)),
		Rows: uint16(defaultShellDimension(rows, 30)),
	})
}

func interactiveShellCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, []string{"-l"}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash", []string{"-l"}
	}
	return "/bin/sh", []string{"-l"}
}

func defaultShellDimension(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func currentShellUsername() string {
	if current, err := user.Current(); err == nil && strings.TrimSpace(current.Username) != "" {
		return current.Username
	}
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("USERNAME")); value != "" {
		return value
	}
	return "server"
}
