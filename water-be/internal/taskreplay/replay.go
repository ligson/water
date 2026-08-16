package taskreplay

import (
	"encoding/json"
	"strings"

	"github.com/ligson/water/water-be/internal/event"
)

type Report struct {
	Score               int      `json:"score"`
	Turns               int      `json:"turns"`
	CompletedTurns      int      `json:"completedTurns"`
	InterruptedTurns    int      `json:"interruptedTurns"`
	PausedTurns         int      `json:"pausedTurns"`
	FailedTurns         int      `json:"failedTurns"`
	ToolCalls           int      `json:"toolCalls"`
	ToolFailures        int      `json:"toolFailures"`
	CorrectedToolCalls  int      `json:"correctedToolCalls"`
	CachedReads         int      `json:"cachedReads"`
	StructuredErrors    int      `json:"structuredErrors"`
	Replans             int      `json:"replans"`
	RecoverySuggestions int      `json:"recoverySuggestions"`
	RepeatedReads       int      `json:"repeatedReads"`
	Writes              int      `json:"writes"`
	Validations         int      `json:"validations"`
	FailedValidations   int      `json:"failedValidations"`
	EndToEndVerified    bool     `json:"endToEndVerified"`
	Findings            []string `json:"findings"`
}

func Assess(events []event.Event) Report {
	report := Report{}
	turns := make(map[string]struct{})
	readsByTask := make(map[string]int)
	for _, item := range events {
		if item.TurnID != "" {
			turns[item.TurnID] = struct{}{}
		}
		switch item.Type {
		case "turn.completed":
			report.CompletedTurns++
		case "turn.interrupted":
			report.InterruptedTurns++
		case "turn.failed":
			report.FailedTurns++
		case "turn.paused":
			report.PausedTurns++
		case "tool.call.started":
			report.ToolCalls++
		case "tool.failed":
			report.ToolFailures++
			var failure struct {
				Code string `json:"code"`
			}
			if json.Unmarshal([]byte(item.PayloadJSON), &failure) == nil && failure.Code != "" {
				report.StructuredErrors++
			}
		case "tool.call.corrected":
			report.CorrectedToolCalls++
		case "tool.call.cached":
			report.CachedReads++
		case "agent.replan.requested":
			report.Replans++
		case "agent.recovery.suggested":
			report.RecoverySuggestions++
		case "tool.completed":
			var payload struct {
				Name   string                 `json:"name"`
				Output map[string]interface{} `json:"output"`
			}
			if json.Unmarshal([]byte(item.PayloadJSON), &payload) != nil {
				continue
			}
			path := mapString(payload.Output, "path")
			if (payload.Name == "read_file" || payload.Name == "list_dir") && path != "" {
				readsByTask[path]++
			}
			if payload.Name == "write_file" {
				report.Writes++
			}
			if payload.Name == "run_command" {
				command := strings.ToLower(mapString(payload.Output, "command"))
				output := strings.ToLower(mapString(payload.Output, "output"))
				if looksLikeValidation(command) {
					report.Validations++
					if !outputSucceeded(payload.Output) {
						report.FailedValidations++
					}
				}
				if strings.Contains(output, "water_e2e_ok") || strings.Contains(command, "verify:e2e") {
					report.EndToEndVerified = true
				}
			}
		}
	}
	report.Turns = len(turns)
	for _, count := range readsByTask {
		if count > 1 {
			report.RepeatedReads += count - 1
		}
	}
	report.Score = score(report)
	report.Findings = findings(report)
	return report
}

func score(report Report) int {
	value := 100
	value -= min(30, report.InterruptedTurns+report.PausedTurns+report.FailedTurns*2)
	value -= min(25, report.RepeatedReads*2+report.ToolFailures)
	if report.Validations == 0 {
		value -= 25
	} else if report.FailedValidations > 0 {
		value -= min(20, report.FailedValidations*5)
	}
	if !report.EndToEndVerified {
		value -= 20
	}
	if value < 0 {
		return 0
	}
	return value
}

func findings(report Report) []string {
	var values []string
	if report.InterruptedTurns+report.PausedTurns+report.FailedTurns > 0 {
		values = append(values, "任务多次失败或中断，缺少稳定收敛路径")
	}
	if report.RepeatedReads > 0 {
		values = append(values, "同一轮重复读取相同资源，信息增益偏低")
	}
	if report.ToolFailures > 0 && report.CorrectedToolCalls+report.Replans == 0 {
		values = append(values, "工具失败后没有观察到自动纠正或替代路径")
	}
	if report.Validations == 0 {
		values = append(values, "没有观察到测试、构建或验收命令")
	}
	if !report.EndToEndVerified {
		values = append(values, "没有完成端到端验收")
	}
	if len(values) == 0 {
		values = append(values, "任务执行链路完整")
	}
	return values
}

func mapString(values map[string]interface{}, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func outputSucceeded(values map[string]interface{}) bool {
	if values == nil {
		return false
	}
	if success, exists := values["success"]; exists {
		if value, ok := success.(bool); ok && !value {
			return false
		}
	}
	return mapString(values, "error") == ""
}

func looksLikeValidation(command string) bool {
	for _, needle := range []string{"go test", "go build", "npm run build", "npm test", "mvn test", "mvn verify", "mvn package", "gradle test", "pytest", "cargo test", "dotnet test", "make test", "verify:", "verify-", "acceptance"} {
		if strings.Contains(command, needle) {
			return true
		}
	}
	return false
}
