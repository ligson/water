package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/workspace"
)

func TestStringifyToolOutputForLLMLimitsLongCommandOutput(t *testing.T) {
	content := stringifyToolOutputForLLM(map[string]interface{}{
		"command": "top -l 1 -s 0",
		"output":  strings.Repeat("x", 7000),
	})

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("decode limited output: %v", err)
	}
	if len(decoded["output"].(string)) != 6000 {
		t.Fatalf("expected output to be limited to 6000 chars, got %d", len(decoded["output"].(string)))
	}
	if decoded["outputTruncatedForLLM"] != true {
		t.Fatalf("expected truncation marker")
	}
}

func TestAssistantPromisedToolUse(t *testing.T) {
	if !assistantPromisedToolUse("让我先看看你的当前工作空间，看看有没有相关系统信息文件需要处理：") {
		t.Fatalf("expected workspace inspection promise to require tool use")
	}
	if assistantPromisedToolUse("vd 开头的设备通常是 virtio 虚拟磁盘设备。") {
		t.Fatalf("expected direct answer not to require tool use")
	}
}

func TestDocumentOutputRequestGetsDefaultPath(t *testing.T) {
	if !isDocumentOutputRequest("帮我总结电脑分析报告并生成文档") {
		t.Fatalf("expected report document request to be detected")
	}
	if isDocumentOutputRequest("查询一下 CPU 使用率") {
		t.Fatalf("expected plain system query to stay non-document")
	}

	path := defaultDocumentPath("/workspace/water", "电脑分析报告", 2)
	expected := filepath.Join("/workspace/water", "reports", "电脑分析报告-turn-2.md")
	if path != expected {
		t.Fatalf("expected default document path %q, got %q", expected, path)
	}
}

func TestBuildTaskRollingSummaryFromEvents(t *testing.T) {
	ws := workspace.Workspace{RootPath: "/workspace/water"}
	summaryEvent := turnSummaryPayload{
		ChangedFiles: []turnSummaryFile{
			{
				Path:      "/workspace/water/water-fe/src/App.vue",
				Action:    "modified",
				Additions: 12,
				Deletions: 3,
			},
		},
		Validations: []turnSummaryCommand{
			{
				Command: "npm run build",
				Status:  "passed",
				Summary: "✓ built in 200ms",
			},
		},
	}
	events := []event.Event{
		{
			Type:        "turn.started",
			PayloadJSON: mustTestJSON(t, map[string]interface{}{"userInput": "把前端设置页整理得更清楚"}),
		},
		{
			Type:        "turn.summary",
			PayloadJSON: mustTestJSON(t, summaryEvent),
		},
	}

	summary := buildTaskRollingSummary(ws, events)
	for _, expected := range []string{
		"最近用户目标",
		"把前端设置页整理得更清楚",
		"water-fe/src/App.vue",
		"modified, +12/-3",
		"npm run build: passed",
		"信息不足时先读相关文件或向用户确认",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected summary to contain %q, got:\n%s", expected, summary)
		}
	}
}

func mustTestJSON(t *testing.T, value interface{}) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(raw)
}
