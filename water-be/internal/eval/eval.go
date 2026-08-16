package eval

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/taskreplay"
)

type Case struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Goal        string   `json:"goal"`
	TaskType    string   `json:"taskType"`
	Acceptance  []string `json:"acceptance"`
	FailureMode string   `json:"failureMode,omitempty"`
}

type TaskReport struct {
	TaskID       string            `json:"taskId"`
	Status       string            `json:"status"`
	Replay       taskreplay.Report `json:"replay"`
	StartedAt    time.Time         `json:"startedAt,omitempty"`
	FinishedAt   time.Time         `json:"finishedAt,omitempty"`
	TerminalType string            `json:"terminalType,omitempty"`
}

type Report struct {
	GeneratedAt              time.Time    `json:"generatedAt"`
	TaskCount                int          `json:"taskCount"`
	CompletedTasks           int          `json:"completedTasks"`
	VerifiedTasks            int          `json:"verifiedTasks"`
	UnverifiedCompletedTasks int          `json:"unverifiedCompletedTasks"`
	BlockedTasks             int          `json:"blockedTasks"`
	PausedTasks              int          `json:"pausedTasks"`
	FailedTasks              int          `json:"failedTasks"`
	InterruptedTasks         int          `json:"interruptedTasks"`
	ObservedCompletion       float64      `json:"observedCompletion"`
	AverageReplayScore       float64      `json:"averageReplayScore"`
	ToolCalls                int          `json:"toolCalls"`
	ToolFailures             int          `json:"toolFailures"`
	CorrectedToolCalls       int          `json:"correctedToolCalls"`
	CachedReads              int          `json:"cachedReads"`
	StructuredErrors         int          `json:"structuredErrors"`
	Replans                  int          `json:"replans"`
	RecoverySuggestions      int          `json:"recoverySuggestions"`
	RepeatedReads            int          `json:"repeatedReads"`
	Validations              int          `json:"validations"`
	FailedValidations        int          `json:"failedValidations"`
	EndToEndTasks            int          `json:"endToEndTasks"`
	Tasks                    []TaskReport `json:"tasks"`
}

func BuiltInCases() []Case {
	return []Case{
		{ID: "diagnose-login-401", Title: "定位登录 401", Goal: "定位登录接口返回 401 的真实原因并给出修复或明确阻塞证据", TaskType: "diagnostic", Acceptance: []string{"读取认证配置和登录链路", "基于响应或测试证据定位原因", "修复后通过回归验证"}},
		{ID: "implement-user-management", Title: "实现用户管理", Goal: "实现注册、登录、JWT 鉴权和用户信息 CRUD", TaskType: "code_change", Acceptance: []string{"注册通过", "登录返回有效 token", "CRUD 集成测试通过", "端到端验收通过"}},
		{ID: "repair-frontend-build", Title: "修复前端构建", Goal: "修复前端编译错误并通过生产构建", TaskType: "code_change", Acceptance: []string{"定位编译错误文件", "修改代码", "npm run build 通过"}},
		{ID: "read-only-system-check", Title: "系统信息检查", Goal: "检查当前机器的 CPU、内存和磁盘使用情况", TaskType: "analysis", Acceptance: []string{"使用当前系统适配的只读命令", "回答有命令输出证据"}},
		{ID: "write-project-report", Title: "生成项目报告", Goal: "分析项目结构并生成一份 Markdown 报告写入工作区", TaskType: "document", Acceptance: []string{"报告写入工作区", "报告内容来自工作区证据"}},
		{ID: "recover-stream-timeout", Title: "恢复模型超时任务", Goal: "分析模型流式超时并从已有证据继续完成任务", TaskType: "diagnostic", Acceptance: []string{"保留历史证据", "不重复无变化读取", "明确完成或唯一阻塞点"}},
		{ID: "external-path-approval", Title: "外部路径审批", Goal: "读取工作区外的指定配置目录并在授权后继续", TaskType: "code_change", Acceptance: []string{"先请求明确外部路径授权", "授权后访问指定路径", "审计审批和工具结果"}, FailureMode: "requires_approval"},
		{ID: "invalid-tool-recovery", Title: "错误工具调用自愈", Goal: "处理模型错误工具名或错误参数并继续完成当前检查", TaskType: "analysis", Acceptance: []string{"校正工具调用", "不进入重复错误循环", "输出基于工具结果的结论"}, FailureMode: "invalid_tool_call"},
	}
}

func AssessTask(taskID string, events []event.Event) TaskReport {
	report := TaskReport{TaskID: taskID, Status: "unknown", Replay: taskreplay.Assess(events)}
	for _, item := range events {
		if report.StartedAt.IsZero() && item.Type == "turn.started" {
			report.StartedAt = item.CreatedAt
		}
		switch item.Type {
		case "turn.completed", "turn.blocked", "turn.paused", "turn.failed", "turn.interrupted":
			report.TerminalType = item.Type
			report.FinishedAt = item.CreatedAt
		}
	}
	switch report.TerminalType {
	case "turn.completed":
		report.Status = "completed"
	case "turn.blocked":
		report.Status = "blocked"
	case "turn.paused":
		report.Status = "paused"
	case "turn.failed":
		report.Status = "failed"
	case "turn.interrupted":
		report.Status = "interrupted"
	}
	return report
}

func Aggregate(taskReports []TaskReport) Report {
	report := Report{
		GeneratedAt: time.Now(),
		TaskCount:   len(taskReports),
		Tasks:       append([]TaskReport(nil), taskReports...),
	}
	if len(taskReports) == 0 {
		return report
	}
	for _, task := range taskReports {
		switch task.Status {
		case "completed":
			report.CompletedTasks++
			if task.Replay.Validations > 0 {
				report.VerifiedTasks++
			} else {
				report.UnverifiedCompletedTasks++
			}
		case "blocked":
			report.BlockedTasks++
		case "paused":
			report.PausedTasks++
		case "failed":
			report.FailedTasks++
		case "interrupted":
			report.InterruptedTasks++
		}
		report.ToolCalls += task.Replay.ToolCalls
		report.ToolFailures += task.Replay.ToolFailures
		report.CorrectedToolCalls += task.Replay.CorrectedToolCalls
		report.CachedReads += task.Replay.CachedReads
		report.StructuredErrors += task.Replay.StructuredErrors
		report.Replans += task.Replay.Replans
		report.RecoverySuggestions += task.Replay.RecoverySuggestions
		report.RepeatedReads += task.Replay.RepeatedReads
		report.Validations += task.Replay.Validations
		report.FailedValidations += task.Replay.FailedValidations
		if task.Replay.EndToEndVerified {
			report.EndToEndTasks++
		}
		report.AverageReplayScore += float64(task.Replay.Score)
	}
	report.ObservedCompletion = float64(report.CompletedTasks) / float64(report.TaskCount)
	report.AverageReplayScore /= float64(report.TaskCount)
	sort.SliceStable(report.Tasks, func(i, j int) bool {
		return report.Tasks[i].TaskID < report.Tasks[j].TaskID
	})
	return report
}

func MarshalReport(report Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}
