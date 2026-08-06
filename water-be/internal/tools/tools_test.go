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
