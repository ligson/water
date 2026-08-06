package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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
