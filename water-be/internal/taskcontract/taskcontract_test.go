package taskcontract

import "testing"

func TestBuildClassifiesLegacyTechnicalWritingGoalAsCodeChange(t *testing.T) {
	contract := Build("task-1", "帮我试用 Spring Boot 写一个用户登录、注册、用户管理功能，前后端分目录")
	if contract.TaskType != TypeCodeChange {
		t.Fatalf("expected technical implementation goal to be code_change, got %q", contract.TaskType)
	}
}

func TestBuildKeepsDocumentWritingAsDocument(t *testing.T) {
	contract := Build("task-1", "帮我写一个项目 README 文档")
	if contract.TaskType != TypeDocument {
		t.Fatalf("expected document goal to stay document, got %q", contract.TaskType)
	}
}
