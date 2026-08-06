package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/approval"
	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/sandbox"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/workspace"
)

const (
	NameListDir    = "list_dir"
	NameReadFile   = "read_file"
	NameWriteFile  = "write_file"
	NameRunCommand = "run_command"
)

var ErrApprovalRequired = errors.New("approval required")

type Executor struct {
	permissions   *sandbox.PermissionEngine
	approvalStore *approval.Store
}

type Request struct {
	RequestID  string          `json:"requestId"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	ApprovalID string          `json:"approvalId"`
	ToolCallID string          `json:"toolCallId,omitempty"`
}

type Context struct {
	Workspace workspace.Workspace
	Task      task.Task
	TurnID    string
}

type Result struct {
	Name     string             `json:"name"`
	Approved bool               `json:"approved"`
	Output   interface{}        `json:"output,omitempty"`
	Approval *approval.Approval `json:"approval,omitempty"`
}

func NewExecutor(permissions *sandbox.PermissionEngine, approvalStore *approval.Store) *Executor {
	return &Executor{permissions: permissions, approvalStore: approvalStore}
}

func Definitions() []llm.Tool {
	return []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        NameListDir,
				Description: "列出指定目录中的文件和子目录。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"要列出的绝对路径"}},"required":["path"],"additionalProperties":false}`),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        NameReadFile,
				Description: "读取指定文件内容。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"要读取的绝对路径"}},"required":["path"],"additionalProperties":false}`),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        NameWriteFile,
				Description: "写入或覆盖指定文件内容。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"要写入的绝对路径"},"content":{"type":"string","description":"要写入的文件内容"}},"required":["path","content"],"additionalProperties":false}`),
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        NameRunCommand,
				Description: "在工作区目录执行有明确结束条件的命令。用于查看系统信息、运行测试或构建；不要用它启动长驻开发服务，例如 npm run dev、vite、mvn spring-boot:run。高风险命令会进入审批流程。常见只读系统信息示例：磁盘 df -h /，macOS 内存 vm_stat 与 sysctl hw.memsize，Linux 内存 free -h，Windows 内存 wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value，CPU 使用率 macOS top -l 1 -s 0 -n 0，Linux top -bn1，Windows wmic cpu get loadpercentage /Value。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"要执行的 shell 命令"},"workingDir":{"type":"string","description":"执行目录，默认为工作区根目录"},"timeoutMs":{"type":"integer","description":"超时时间，单位毫秒"}},"required":["command"],"additionalProperties":false}`),
			},
		},
	}
}

func (e *Executor) Execute(ctx context.Context, toolCtx Context, req Request) (Result, error) {
	name := strings.TrimSpace(req.Name)
	switch name {
	case NameListDir:
		return e.listDir(ctx, toolCtx, req)
	case NameReadFile:
		return e.readFile(ctx, toolCtx, req)
	case NameWriteFile:
		return e.writeFile(ctx, toolCtx, req)
	case NameRunCommand:
		return e.runCommand(ctx, toolCtx, req)
	default:
		return Result{}, fmt.Errorf("unsupported tool %q", name)
	}
}

func (e *Executor) listDir(ctx context.Context, toolCtx Context, req Request) (Result, error) {
	var args pathArgs
	if err := decodeArgs(req.Arguments, &args); err != nil {
		return Result{}, err
	}
	path, err := e.permissions.CheckPath(ctx, toolCtx.Workspace, args.Path, sandbox.AccessRead)
	if err != nil {
		return Result{}, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return Result{}, fmt.Errorf("list directory: %w", err)
	}
	items := make([]dirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return Result{}, fmt.Errorf("read directory entry: %w", err)
		}
		items = append(items, dirEntry{
			Name:  entry.Name(),
			Path:  filepath.Join(path, entry.Name()),
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}
	return Result{Name: NameListDir, Approved: true, Output: map[string]interface{}{"path": path, "items": items}}, nil
}

func (e *Executor) readFile(ctx context.Context, toolCtx Context, req Request) (Result, error) {
	var args pathArgs
	if err := decodeArgs(req.Arguments, &args); err != nil {
		return Result{}, err
	}
	path, err := e.permissions.CheckPath(ctx, toolCtx.Workspace, args.Path, sandbox.AccessRead)
	if err != nil {
		return Result{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}
	return Result{Name: NameReadFile, Approved: true, Output: map[string]interface{}{"path": path, "content": string(content)}}, nil
}

func (e *Executor) writeFile(ctx context.Context, toolCtx Context, req Request) (Result, error) {
	var args writeFileArgs
	if err := decodeArgs(req.Arguments, &args); err != nil {
		return Result{}, err
	}
	path, err := e.permissions.CheckPath(ctx, toolCtx.Workspace, args.Path, sandbox.AccessWrite)
	if err != nil {
		return Result{}, err
	}
	if err := e.ensureApproved(ctx, toolCtx, req, approval.ActionWriteFile, path, "写入文件会改变磁盘内容", "目标文件会被新内容覆盖或创建"); err != nil {
		if appr := approvalFromErr(err); appr != nil {
			return Result{Name: NameWriteFile, Approved: false, Approval: appr}, ErrApprovalRequired
		}
		return Result{}, err
	}

	previousContent, readErr := os.ReadFile(path)
	created := false
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return Result{}, fmt.Errorf("read existing file: %w", readErr)
		}
		created = true
	}
	additions, deletions := lineDiffStats(string(previousContent), args.Content, created)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("create parent directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}
	action := "modified"
	if created {
		action = "created"
	}
	return Result{Name: NameWriteFile, Approved: true, Output: map[string]interface{}{
		"path":      path,
		"bytes":     len([]byte(args.Content)),
		"action":    action,
		"additions": additions,
		"deletions": deletions,
	}}, nil
}

func lineDiffStats(oldContent string, newContent string, created bool) (int, int) {
	newLines := splitComparableLines(newContent)
	if created {
		return len(newLines), 0
	}
	oldLines := splitComparableLines(oldContent)
	if oldContent == newContent {
		return 0, 0
	}
	if len(oldLines) == 0 {
		return len(newLines), 0
	}
	if len(newLines) == 0 {
		return 0, len(oldLines)
	}
	if len(oldLines) > 2000 || len(newLines) > 2000 {
		return len(newLines), len(oldLines)
	}
	common := longestCommonSubsequenceLineCount(oldLines, newLines)
	return len(newLines) - common, len(oldLines) - common
}

func splitComparableLines(content string) []string {
	if content == "" {
		return nil
	}
	normalized := strings.TrimSuffix(content, "\n")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func longestCommonSubsequenceLineCount(a []string, b []string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				current[j] = previous[j-1] + 1
			} else if previous[j] >= current[j-1] {
				current[j] = previous[j]
			} else {
				current[j] = current[j-1]
			}
		}
		previous, current = current, previous
		for j := range current {
			current[j] = 0
		}
	}
	return previous[len(b)]
}

func (e *Executor) runCommand(ctx context.Context, toolCtx Context, req Request) (Result, error) {
	var args commandArgs
	if err := decodeArgs(req.Arguments, &args); err != nil {
		return Result{}, err
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return Result{}, errors.New("command is required")
	}

	if hasBackgroundOperator(args.Command) {
		return Result{}, errors.New("background commands using '&' are not supported; use a managed dev-server tool or run a bounded foreground command")
	}
	if isLongRunningDevServerCommand(args.Command) {
		return Result{}, errors.New("普通 run_command 不支持长驻开发服务命令；请使用受管理的 dev-server 工具，或改为 npm run build / mvn test 这类有明确结束条件的命令")
	}
	if err := validateScaffoldCommand(args.Command, toolCtx.Workspace.RootPath); err != nil {
		return Result{}, err
	}
	if err := e.validateCommandPaths(ctx, toolCtx, args.Command); err != nil {
		return Result{}, err
	}

	workingDir := toolCtx.Workspace.RootPath
	if strings.TrimSpace(args.WorkingDir) != "" {
		checked, err := e.permissions.CheckPath(ctx, toolCtx.Workspace, args.WorkingDir, sandbox.AccessRead)
		if err != nil {
			return Result{}, err
		}
		workingDir = checked
	}

	if !isSafeReadOnlyCommand(args.Command) {
		if err := e.ensureApproved(ctx, toolCtx, req, approval.ActionRunCommand, args.Command, "命令执行可能修改文件、启动进程或访问网络", "命令将在工作目录 "+workingDir+" 中运行"); err != nil {
			if appr := approvalFromErr(err); appr != nil {
				return Result{Name: NameRunCommand, Approved: false, Approval: appr}, ErrApprovalRequired
			}
			return Result{}, err
		}
	}

	timeout := time.Duration(args.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shellName, shellArgs := shellCommand(runtime.GOOS, args.Command)
	cmd := exec.CommandContext(runCtx, shellName, shellArgs...)
	cmd.Dir = workingDir
	configureCommandProcessGroup(cmd)
	cmd.Cancel = func() error {
		return terminateCommandProcessGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	truncated := false
	if len(output) > 64*1024 {
		output = output[:64*1024]
		truncated = true
	}
	result := map[string]interface{}{
		"command":    args.Command,
		"workingDir": workingDir,
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"output":     string(output),
		"truncated":  truncated,
	}
	if err != nil {
		result["error"] = err.Error()
		if isCommandNotFound(output) {
			result["hint"] = commandNotFoundHint(args.Command)
		}
	}
	return Result{Name: NameRunCommand, Approved: true, Output: result}, nil
}

func hasBackgroundOperator(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	if strings.HasSuffix(trimmed, "&") && !strings.HasSuffix(trimmed, "&&") {
		return true
	}
	for _, field := range strings.Fields(trimmed) {
		if field == "&" {
			return true
		}
	}
	return false
}

func isLongRunningDevServerCommand(command string) bool {
	fields := normalizedCommandFields(command)
	for i := 0; i < len(fields); i++ {
		if isShellControlToken(fields[i]) {
			continue
		}
		if matchesLongRunningDevServer(fields[i:]) {
			return true
		}
	}
	return false
}

func matchesLongRunningDevServer(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "npm":
		return len(fields) >= 3 && fields[1] == "run" && isDevServerScript(fields[2]) ||
			len(fields) >= 2 && fields[1] == "start"
	case "pnpm":
		return len(fields) >= 2 && isDevServerScript(fields[1]) ||
			len(fields) >= 3 && fields[1] == "run" && isDevServerScript(fields[2])
	case "yarn":
		return len(fields) >= 2 && isDevServerScript(fields[1]) ||
			len(fields) >= 3 && fields[1] == "run" && isDevServerScript(fields[2])
	case "vite":
		return true
	case "npx":
		return len(fields) >= 2 && fields[1] == "vite"
	case "mvn", "mvnw", "./mvnw":
		return containsField(fields[1:], "spring-boot:run")
	case "gradle", "./gradlew", "gradlew":
		return containsField(fields[1:], "bootrun")
	case "python", "python3":
		return len(fields) >= 4 && fields[1] == "-m" && fields[2] == "http.server" ||
			len(fields) >= 3 && fields[1] == "-m" && fields[2] == "uvicorn"
	case "uvicorn":
		return true
	case "flask":
		return len(fields) >= 2 && fields[1] == "run"
	case "streamlit":
		return len(fields) >= 2 && fields[1] == "run"
	case "next", "nuxt":
		return len(fields) >= 2 && fields[1] == "dev"
	}
	return false
}

func normalizedCommandFields(command string) []string {
	rawFields := strings.Fields(strings.ToLower(command))
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		field = strings.Trim(field, `"'`)
		field = strings.TrimSuffix(field, ";")
		if field != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

func isShellControlToken(field string) bool {
	switch field {
	case "cd", "&&", "||", ";", "|", "2>&1", ">", ">>", "<":
		return true
	default:
		return false
	}
}

func isDevServerScript(script string) bool {
	script = strings.Trim(script, `"'`)
	switch script {
	case "dev", "start", "serve", "preview":
		return true
	default:
		return false
	}
}

func containsField(fields []string, target string) bool {
	for _, field := range fields {
		if strings.Trim(field, `"'`) == target {
			return true
		}
	}
	return false
}

func validateScaffoldCommand(command string, workspaceRoot string) error {
	root := filepath.Clean(workspaceRoot)
	for _, target := range viteScaffoldTargets(command) {
		if !filepath.IsAbs(target) {
			continue
		}
		cleaned := filepath.Clean(target)
		if cleaned == root || strings.HasPrefix(cleaned, root+string(os.PathSeparator)) {
			return fmt.Errorf("scaffold target must be a relative path inside the working directory; avoid passing workspace absolute path %q to create-vite because it can create nested Users/... directories", target)
		}
	}
	return nil
}

func viteScaffoldTargets(command string) []string {
	rawFields := strings.Fields(command)
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		fields = append(fields, strings.Trim(field, `"'`))
	}
	targets := make([]string, 0, 1)
	for i, field := range fields {
		field = strings.ToLower(field)
		switch field {
		case "create-vite":
			targets = appendScaffoldTarget(targets, fields, i+1)
		case "npm", "pnpm", "yarn":
			if i+2 >= len(fields) {
				continue
			}
			action := strings.ToLower(fields[i+1])
			template := strings.ToLower(fields[i+2])
			if (action == "create" || action == "init") && (template == "vite" || strings.HasPrefix(template, "vite@")) {
				targets = appendScaffoldTarget(targets, fields, i+3)
			}
		case "npx":
			if i+1 >= len(fields) {
				continue
			}
			template := strings.ToLower(fields[i+1])
			if template == "create-vite" || strings.HasPrefix(template, "create-vite@") {
				targets = appendScaffoldTarget(targets, fields, i+2)
			}
		}
	}
	return targets
}

func appendScaffoldTarget(targets []string, fields []string, start int) []string {
	for i := start; i < len(fields); i++ {
		field := fields[i]
		if field == "" || field == "--" {
			return targets
		}
		if field == "&&" || field == "||" || field == ";" || field == "|" {
			return targets
		}
		if strings.HasPrefix(field, "-") {
			continue
		}
		return append(targets, field)
	}
	return targets
}

func (e *Executor) validateCommandPaths(ctx context.Context, toolCtx Context, command string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return errors.New("command is required")
	}
	switch fields[0] {
	case "df":
		return nil
	}
	for _, field := range fields[1:] {
		if !filepath.IsAbs(strings.Trim(field, `"'`)) {
			continue
		}
		cleaned := strings.Trim(field, `"'`)
		if _, err := e.permissions.CheckPath(ctx, toolCtx.Workspace, cleaned, sandbox.AccessRead); err != nil {
			return err
		}
	}
	return nil
}

func shellCommand(goos string, command string) (string, []string) {
	if goos == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

func isCommandNotFound(output []byte) bool {
	return strings.Contains(strings.ToLower(string(output)), "command not found")
}

func commandNotFoundHint(command string) string {
	first := ""
	if fields := strings.Fields(command); len(fields) > 0 {
		first = fields[0]
	}
	if runtime.GOOS == "darwin" && first == "free" {
		return "当前系统是 macOS(darwin)，没有 free 命令。查询内存请尝试 vm_stat，并用 sysctl hw.memsize 查询总内存。"
	}
	if runtime.GOOS == "linux" && (first == "vm_stat" || first == "sysctl") {
		return "当前系统是 Linux。查询内存请尝试 free -h，或读取 cat /proc/meminfo。"
	}
	if runtime.GOOS == "windows" && (first == "free" || first == "vm_stat") {
		return "当前系统是 Windows。查询内存请尝试 wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value，或使用受限 PowerShell 查询 Get-CimInstance Win32_OperatingSystem。"
	}
	if runtime.GOOS == "darwin" && (first == "mpstat" || first == "wmic") {
		return "当前系统是 macOS(darwin)。查询 CPU 使用率请尝试 top -l 1 -s 0 -n 0。"
	}
	if runtime.GOOS == "linux" && (first == "wmic" || first == "sysctl") {
		return "当前系统是 Linux。查询 CPU 使用率请尝试 top -bn1，或在安装 sysstat 时使用 mpstat 1 1。"
	}
	return "命令不存在。请根据输出中的 os 字段选择当前系统支持的只读替代命令。"
}

// isSafeReadOnlyCommand is intentionally conservative. It only auto-allows
// obviously read-only inspection commands, leaving everything else for approval.
func isSafeReadOnlyCommand(command string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return false
	}
	if strings.ContainsAny(command, "&;|`><") || strings.Contains(command, "$(") || strings.Contains(command, "\n") || strings.Contains(command, "\r") {
		return false
	}

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "pwd":
		return len(fields) == 1
	case "df":
		return len(fields) >= 2 && fields[1] == "-h"
	case "diskutil":
		return len(fields) >= 3 && fields[1] == "info"
	case "vm_stat":
		return len(fields) == 1
	case "sysctl":
		return len(fields) == 2 && fields[1] == "hw.memsize" ||
			len(fields) == 3 && fields[1] == "-n" && fields[2] == "hw.memsize"
	case "free":
		return len(fields) == 2 && fields[1] == "-h"
	case "top":
		return isSafeTopCommand(fields)
	case "mpstat":
		return len(fields) == 3 && fields[1] == "1" && fields[2] == "1"
	case "memory_pressure":
		return len(fields) == 1
	case "uptime":
		return len(fields) == 1
	case "uname":
		return len(fields) == 2 && fields[1] == "-a"
	case "sw_vers":
		return len(fields) == 1
	case "cat":
		return len(fields) == 2 && fields[1] == "/proc/meminfo"
	case "wmic":
		return isSafeWMICCommand(fields)
	case "systeminfo":
		return len(fields) == 1
	case "ver":
		return len(fields) == 1
	case "powershell", "powershell.exe":
		return isSafePowerShellCommand(fields)
	case "ls":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status":
			return true
		case "branch":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func isSafeTopCommand(fields []string) bool {
	if len(fields) == 7 && fields[1] == "-l" && fields[2] == "1" && fields[3] == "-s" && fields[4] == "0" && fields[5] == "-n" && fields[6] == "0" {
		return true
	}
	if len(fields) == 5 && fields[1] == "-l" && fields[2] == "1" && fields[3] == "-s" && fields[4] == "0" {
		return true
	}
	return len(fields) == 2 && fields[1] == "-bn1"
}

func isSafeWMICCommand(fields []string) bool {
	if len(fields) == 5 && fields[1] == "cpu" && fields[2] == "get" && fields[4] == "/value" {
		return allInSet(strings.Split(fields[3], ","), map[string]bool{
			"loadpercentage": true,
			"name":           true,
			"numberofcores":  true,
		})
	}
	if len(fields) == 5 && fields[1] == "os" && fields[2] == "get" && fields[4] == "/value" {
		return allInSet(strings.Split(fields[3], ","), map[string]bool{
			"freephysicalmemory":     true,
			"totalvisiblememorysize": true,
			"caption":                true,
			"version":                true,
			"osarchitecture":         true,
			"totalvirtualmemorysize": true,
			"freevirtualmemory":      true,
		})
	}
	if len(fields) == 5 && fields[1] == "computersystem" && fields[2] == "get" && fields[4] == "/value" {
		return allInSet(strings.Split(fields[3], ","), map[string]bool{
			"totalphysicalmemory": true,
			"model":               true,
			"manufacturer":        true,
			"systemtype":          true,
		})
	}
	if len(fields) == 4 && fields[1] == "logicaldisk" && fields[2] == "get" {
		return allInSet(strings.Split(fields[3], ","), map[string]bool{
			"caption":   true,
			"size":      true,
			"freespace": true,
		})
	}
	return false
}

func isSafePowerShellCommand(fields []string) bool {
	if len(fields) == 4 {
		return fields[1] == "-noprofile" &&
			fields[2] == "-command" &&
			fields[3] == "get-computerinfo"
	}
	if len(fields) != 5 {
		return false
	}
	return fields[1] == "-noprofile" &&
		fields[2] == "-command" &&
		fields[3] == "get-ciminstance" &&
		fields[4] == "win32_operatingsystem"
}

func allInSet(values []string, allowed map[string]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !allowed[value] {
			return false
		}
	}
	return true
}

func (e *Executor) ensureApproved(ctx context.Context, toolCtx Context, req Request, actionType string, target string, risk string, impact string) error {
	if toolCtx.Workspace.PermissionMode == workspace.PermissionModeFullAccess {
		return nil
	}
	if req.ApprovalID != "" {
		appr, err := e.approvalStore.Get(ctx, req.ApprovalID)
		if err != nil {
			return err
		}
		if appr.Status != approval.StatusApproved || appr.WorkspaceID != toolCtx.Workspace.ID || appr.TaskID != toolCtx.Task.ID || appr.ActionType != actionType {
			return errors.New("approval does not allow this action")
		}
		if !approvalRequestMatches(appr.RequestJSON, req) {
			return errors.New("approval request does not match this tool call")
		}
		return nil
	}
	requestJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode approval request: %w", err)
	}
	appr, err := e.approvalStore.Create(ctx, approval.CreateInput{
		WorkspaceID:    toolCtx.Workspace.ID,
		TaskID:         toolCtx.Task.ID,
		TurnID:         toolCtx.TurnID,
		ActionType:     actionType,
		Target:         target,
		RiskSummary:    risk,
		ExpectedImpact: impact,
		RequestJSON:    string(requestJSON),
	})
	if err != nil {
		return err
	}
	return approvalRequiredError{approval: appr}
}

func approvalRequestMatches(snapshot string, req Request) bool {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" || snapshot == "{}" {
		return true
	}
	var saved Request
	if err := json.Unmarshal([]byte(snapshot), &saved); err != nil {
		return false
	}
	if strings.TrimSpace(saved.Name) != strings.TrimSpace(req.Name) {
		return false
	}
	return normalizeJSON(saved.Arguments) == normalizeJSON(req.Arguments)
}

func normalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(normalized)
}

type approvalRequiredError struct {
	approval approval.Approval
}

func (e approvalRequiredError) Error() string {
	return ErrApprovalRequired.Error()
}

func approvalFromErr(err error) *approval.Approval {
	var required approvalRequiredError
	if errors.As(err, &required) {
		return &required.approval
	}
	return nil
}

type pathArgs struct {
	Path string `json:"path"`
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type commandArgs struct {
	Command    string `json:"command"`
	WorkingDir string `json:"workingDir"`
	TimeoutMS  int    `json:"timeoutMs"`
}

type dirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

func decodeArgs(raw json.RawMessage, target interface{}) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}
