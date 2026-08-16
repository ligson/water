package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ligson/water/water-be/internal/document"
	"github.com/ligson/water/water-be/internal/sandbox"
	"github.com/ligson/water/water-be/internal/skill"
	"github.com/ligson/water/water-be/internal/workspace"
)

func TestSafeReadOnlyCommandAllowsMemoryInspection(t *testing.T) {
	allowed := []string{
		"vm_stat",
		"sysctl hw.memsize",
		"sysctl -n hw.memsize",
		"free -h",
		"cat /proc/meminfo",
		"memory_pressure",
		"wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value",
		"wmic ComputerSystem get TotalPhysicalMemory /Value",
		"powershell -NoProfile -Command Get-CimInstance Win32_OperatingSystem",
	}

	for _, command := range allowed {
		if !isSafeReadOnlyCommand(command) {
			t.Fatalf("expected %q to be allowed", command)
		}
	}
}

func TestSafeReadOnlyCommandAllowsCPUInspection(t *testing.T) {
	allowed := []string{
		"top -l 1 -s 0 -n 0",
		"top -l 1 -s 0",
		"top -bn1",
		"mpstat 1 1",
		"wmic cpu get loadpercentage /Value",
		"wmic cpu get loadpercentage,name,numberofcores /Value",
	}

	for _, command := range allowed {
		if !isSafeReadOnlyCommand(command) {
			t.Fatalf("expected %q to be allowed", command)
		}
	}
}

func TestSafeReadOnlyCommandRejectsShellComposition(t *testing.T) {
	rejected := []string{
		"vm_stat; rm -rf /tmp/water",
		"sysctl hw.memsize && whoami",
		"free -h | cat",
		"cat /etc/passwd",
		"top -l 1 | head",
		"mpstat 1 1; whoami",
		"wmic cpu get loadpercentage /Value & whoami",
		"powershell -NoProfile -Command Remove-Item C:\\temp",
		"wmic process call create calc.exe",
	}

	for _, command := range rejected {
		if isSafeReadOnlyCommand(command) {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestCommandNotFoundHint(t *testing.T) {
	if !isCommandNotFound([]byte("sh: free: command not found\n")) {
		t.Fatalf("expected command not found output to be detected")
	}
	if commandNotFoundHint("free -h") == "" {
		t.Fatalf("expected non-empty hint")
	}
}

func TestVerificationKindClassifiesStructuredCommandEvidence(t *testing.T) {
	cases := map[string]string{
		"go test ./...":              "test",
		"npm run build":              "build",
		"bash scripts/verify-e2e.sh": "end_to_end",
		"npm run lint":               "lint",
		"git status --short":         "",
	}
	for command, expected := range cases {
		if got := verificationKind(command); got != expected {
			t.Fatalf("command %q: expected verification kind %q, got %q", command, expected, got)
		}
	}
}

func TestBackgroundOperatorDetection(t *testing.T) {
	rejected := []string{
		"mvn spring-boot:run &",
		"npm run dev 2>&1 &",
		"npm run dev & echo started",
	}
	for _, command := range rejected {
		if !hasBackgroundOperator(command) {
			t.Fatalf("expected %q to be rejected as background command", command)
		}
	}

	allowed := []string{
		"npm run build 2>&1 | tail -20",
		"cd demo-be && mvn compile",
		"echo a && echo b",
	}
	for _, command := range allowed {
		if hasBackgroundOperator(command) {
			t.Fatalf("expected %q not to be treated as background command", command)
		}
	}
}

func TestLongRunningDevServerCommandDetection(t *testing.T) {
	rejected := []string{
		"npm run dev",
		"cd /workspace/demo-fe && npm run dev",
		"npm run dev -- --host 127.0.0.1",
		"pnpm dev",
		"yarn start",
		"npx vite --host 127.0.0.1",
		"vite --host 127.0.0.1",
		"mvn spring-boot:run",
		"cd demo-be && mvn spring-boot:run 2>&1 | tail -30",
		"./mvnw spring-boot:run",
		"./gradlew bootRun",
		"python3 -m http.server 5173",
	}
	for _, command := range rejected {
		if !isLongRunningDevServerCommand(command) {
			t.Fatalf("expected %q to be rejected as long-running dev server command", command)
		}
	}

	allowed := []string{
		"npm run build",
		"npm test",
		"mvn test",
		"mvn -q test",
		"cd demo-be && mvn compile",
		"npm run lint 2>&1 | tail -30",
	}
	for _, command := range allowed {
		if isLongRunningDevServerCommand(command) {
			t.Fatalf("expected %q not to be treated as long-running dev server command", command)
		}
	}
}

func TestValidateScaffoldCommandRejectsAbsoluteViteTarget(t *testing.T) {
	command := "rm -rf /workspace/demo-fe-new && npm create vite@latest /workspace/demo-fe -- --template vue-ts"
	if err := validateScaffoldCommand(command, "/workspace"); err == nil {
		t.Fatalf("expected absolute create-vite target to be rejected")
	}
}

func TestValidateScaffoldCommandAllowsRelativeViteTarget(t *testing.T) {
	allowed := []string{
		"npm create vite@latest demo-fe -- --template vue-ts",
		"cd /workspace && npm create vite@latest demo-fe -- --template vue-ts",
		"npx create-vite demo-fe --template vue-ts",
	}
	for _, command := range allowed {
		if err := validateScaffoldCommand(command, "/workspace"); err != nil {
			t.Fatalf("expected %q to be allowed, got %v", command, err)
		}
	}
}

func TestShellDevicePathsDoNotRequireWorkspaceAuthorization(t *testing.T) {
	for _, path := range []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"} {
		if !isShellDevicePath(path) {
			t.Fatalf("expected %s to be recognized as a shell device", path)
		}
	}
	if isShellDevicePath("/Users/example/output.txt") {
		t.Fatal("expected regular absolute path to remain subject to workspace authorization")
	}
}

func TestShellCommandForOS(t *testing.T) {
	name, args := shellCommand("windows", "wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value")
	if name != "cmd" || len(args) != 2 || args[0] != "/C" {
		t.Fatalf("expected windows cmd shell, got %q %#v", name, args)
	}

	name, args = shellCommand("linux", "free -h")
	if name != "sh" || len(args) != 2 || args[0] != "-c" {
		t.Fatalf("expected unix sh shell, got %q %#v", name, args)
	}
}

func TestLineDiffStats(t *testing.T) {
	additions, deletions := lineDiffStats("a\nb\nc\n", "a\nb2\nc\nd\n", false)
	if additions != 2 || deletions != 1 {
		t.Fatalf("expected +2 -1, got +%d -%d", additions, deletions)
	}

	additions, deletions = lineDiffStats("", "one\n\nthree", true)
	if additions != 3 || deletions != 0 {
		t.Fatalf("expected created file +3 -0, got +%d -%d", additions, deletions)
	}
}

func TestReadFileDirectoryReturnsStructuredCorrection(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(sandbox.NewPermissionEngine(nil), nil)
	_, err := executor.Execute(context.Background(), Context{
		Workspace: workspace.Workspace{RootPath: root},
	}, Request{Name: NameReadFile, Arguments: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("expected structured execution error, got %v", err)
	}
	if executionErr.Code != "target_is_directory" || executionErr.SuggestedTool != NameListDir {
		t.Fatalf("unexpected correction details: %#v", executionErr)
	}
}

type fakeDocumentReader struct {
	result document.Result
	err    error
}

type fakeSkillReader struct {
	item skill.Skill
	err  error
}

func (f fakeSkillReader) GetEnabled(context.Context, string) (skill.Skill, error) {
	return f.item, f.err
}

func (f fakeDocumentReader) Extract(context.Context, string) (document.Result, error) {
	return f.result, f.err
}

func TestReadDocumentReturnsRuneSafeChunk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.docx")
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	executor := NewExecutor(
		sandbox.NewPermissionEngine(nil),
		nil,
		WithDocumentReader(fakeDocumentReader{result: document.Result{
			Content: "甲乙丙丁ABCDE",
			Engine:  "test",
			Format:  "docx",
		}}),
	)
	result, err := executor.Execute(context.Background(), Context{
		Workspace: workspace.Workspace{RootPath: root},
	}, Request{Name: NameReadDocument, Arguments: []byte(`{"path":"` + filepath.ToSlash(path) + `","offset":1,"maxChars":4}`)})
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	output := result.Output.(map[string]interface{})
	if output["content"] != "乙丙丁A" || output["nextOffset"] != 5 || output["truncated"] != true {
		t.Fatalf("unexpected document chunk: %#v", output)
	}
}

func TestReadDocumentRejectsLegacyWordBeforeExtraction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.doc")
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	executor := NewExecutor(sandbox.NewPermissionEngine(nil), nil, WithDocumentReader(fakeDocumentReader{}))
	_, err := executor.Execute(context.Background(), Context{
		Workspace: workspace.Workspace{RootPath: root},
	}, Request{Name: NameReadDocument, Arguments: []byte(`{"path":"` + filepath.ToSlash(path) + `"}`)})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.Code != "unsupported_document_type" {
		t.Fatalf("expected unsupported document error, got %v", err)
	}
}

func TestReadSkillReturnsEnabledInstructions(t *testing.T) {
	executor := NewExecutor(nil, nil, WithSkillReader(fakeSkillReader{item: skill.Skill{
		ID:           "document-review",
		Name:         "文档审阅",
		Version:      "1.0.0",
		Instructions: "按段读取并核对证据。",
		Enabled:      true,
	}}))
	result, err := executor.Execute(context.Background(), Context{}, Request{Name: NameReadSkill, Arguments: []byte(`{"id":"document-review"}`)})
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	output := result.Output.(map[string]interface{})
	if output["id"] != "document-review" || output["instructions"] != "按段读取并核对证据。" {
		t.Fatalf("unexpected skill output: %#v", output)
	}
}

func TestReadSkillRejectsDisabledSkill(t *testing.T) {
	executor := NewExecutor(nil, nil, WithSkillReader(fakeSkillReader{err: skill.ErrDisabled}))
	_, err := executor.Execute(context.Background(), Context{}, Request{Name: NameReadSkill, Arguments: []byte(`{"id":"document-review"}`)})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.Code != "skill_disabled" {
		t.Fatalf("expected disabled skill error, got %v", err)
	}
}

func TestNormalizeRequestRepairsToolAndArgumentAliases(t *testing.T) {
	req, correction := NormalizeRequest(Request{
		Name:      "exec",
		Arguments: []byte(`{"cmd":"go test ./...","working_dir":"/workspace/project","timeout_ms":1000}`),
	}, "/workspace/project")
	if req.Name != NameRunCommand || !correction.Corrected() {
		t.Fatalf("expected command tool correction, got %#v %#v", req, correction)
	}
	var args map[string]interface{}
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		t.Fatalf("decode normalized arguments: %v", err)
	}
	if args["command"] != "go test ./..." || args["workingDir"] != "/workspace/project" || args["timeoutMs"] != float64(1000) {
		t.Fatalf("unexpected normalized arguments: %#v", args)
	}
}

func TestNormalizeDocumentToolAliases(t *testing.T) {
	req, correction := NormalizeRequest(Request{
		Name:      "inspect_document",
		Arguments: []byte(`{"file":"/workspace/report.pdf","max_chars":1200}`),
	}, "/workspace")
	if req.Name != NameReadDocument || !correction.Corrected() {
		t.Fatalf("expected document tool correction, got %#v %#v", req, correction)
	}
	var args map[string]interface{}
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		t.Fatalf("decode normalized arguments: %v", err)
	}
	if args["path"] != "/workspace/report.pdf" || args["maxChars"] != float64(1200) {
		t.Fatalf("unexpected normalized document arguments: %#v", args)
	}
}

func TestNormalizeMissingReadPathListsWorkspace(t *testing.T) {
	req, correction := NormalizeRequest(Request{Name: NameReadFile, Arguments: []byte(`{}`)}, "/workspace/project")
	if req.Name != NameListDir || !correction.Corrected() {
		t.Fatalf("expected missing read path to become safe directory inspection, got %#v %#v", req, correction)
	}
	var args map[string]string
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		t.Fatalf("decode normalized path: %v", err)
	}
	if args["path"] != "/workspace/project" {
		t.Fatalf("expected workspace root path, got %#v", args)
	}
}

func TestUnsupportedToolReturnsStructuredError(t *testing.T) {
	_, err := NewExecutor(nil, nil).Execute(context.Background(), Context{}, Request{Name: "mystery_tool", Arguments: []byte(`{}`)})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.Code != "unsupported_tool" {
		t.Fatalf("expected structured unsupported tool error, got %v", err)
	}
}
