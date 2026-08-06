package tools

import "testing"

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
