package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/taskplan"
	"github.com/ligson/water/water-be/internal/workspace"
)

const (
	toolLoopInputBudgetRatio = 0.68
	toolLoopSummaryMaxTokens = 1200
)

type toolLoopCompactionStats struct {
	OriginalEstimatedTokens  int
	CompactedEstimatedTokens int
	TokenBudget              int
	DroppedMessages          int
}

type toolLoopMessageGroup struct {
	messages []llm.Message
	tokens   int
}

func compactToolLoopMessages(messages []llm.Message, contextWindowTokens int) ([]llm.Message, toolLoopCompactionStats) {
	stats := toolLoopCompactionStats{
		OriginalEstimatedTokens: estimateMessagesTokens(messages),
	}
	if contextWindowTokens <= 0 || len(messages) <= 2 {
		stats.CompactedEstimatedTokens = stats.OriginalEstimatedTokens
		return messages, stats
	}

	stats.TokenBudget = int(float64(contextWindowTokens) * toolLoopInputBudgetRatio)
	if stats.TokenBudget <= 0 || stats.OriginalEstimatedTokens <= stats.TokenBudget {
		stats.CompactedEstimatedTokens = stats.OriginalEstimatedTokens
		return messages, stats
	}

	prefixEnd := 2
	if len(messages) < prefixEnd {
		prefixEnd = len(messages)
	}
	prefix := append([]llm.Message(nil), messages[:prefixEnd]...)
	groups := groupToolLoopMessages(messages[prefixEnd:])
	if len(groups) == 0 {
		stats.CompactedEstimatedTokens = stats.OriginalEstimatedTokens
		return messages, stats
	}

	prefixTokens := estimateMessagesTokens(prefix)
	summaryReserve := min(toolLoopSummaryMaxTokens, max(256, (stats.TokenBudget-prefixTokens)/4))
	remaining := max(0, stats.TokenBudget-prefixTokens-summaryReserve)
	keepFrom := len(groups)
	keptTokens := 0
	for index := len(groups) - 1; index >= 0; index-- {
		groupTokens := groups[index].tokens
		if keepFrom < len(groups) && keptTokens+groupTokens > remaining {
			break
		}
		keepFrom = index
		keptTokens += groupTokens
	}
	if keepFrom == 0 {
		stats.CompactedEstimatedTokens = stats.OriginalEstimatedTokens
		return messages, stats
	}

	dropped := flattenToolLoopGroups(groups[:keepFrom])
	kept := flattenToolLoopGroups(groups[keepFrom:])
	stats.DroppedMessages = len(dropped)
	summary := summarizeDroppedToolLoopMessages(dropped, summaryReserve)
	compacted := make([]llm.Message, 0, len(prefix)+1+len(kept))
	compacted = append(compacted, prefix...)
	compacted = append(compacted, llm.Message{
		Role: llm.RoleSystem,
		Content: "本轮较早的工具上下文已压缩。以下是已经执行过的检查与结果摘要，" +
			"不要换一组 grep、sed、head 或 tail 参数重复读取同一资源；应基于这些事实继续收敛。\n" + summary,
	})
	compacted = append(compacted, kept...)
	stats.CompactedEstimatedTokens = estimateMessagesTokens(compacted)
	return compacted, stats
}

func groupToolLoopMessages(messages []llm.Message) []toolLoopMessageGroup {
	groups := make([]toolLoopMessageGroup, 0)
	for index := 0; index < len(messages); {
		start := index
		index++
		if messages[start].Role == llm.RoleAssistant && len(messages[start].ToolCalls) > 0 {
			for index < len(messages) && messages[index].Role == llm.RoleTool {
				index++
			}
		}
		groupMessages := append([]llm.Message(nil), messages[start:index]...)
		groups = append(groups, toolLoopMessageGroup{
			messages: groupMessages,
			tokens:   estimateMessagesTokens(groupMessages),
		})
	}
	return groups
}

func flattenToolLoopGroups(groups []toolLoopMessageGroup) []llm.Message {
	count := 0
	for _, group := range groups {
		count += len(group.messages)
	}
	messages := make([]llm.Message, 0, count)
	for _, group := range groups {
		messages = append(messages, group.messages...)
	}
	return messages
}

func summarizeDroppedToolLoopMessages(messages []llm.Message, maxTokens int) string {
	var lines []string
	for _, message := range messages {
		switch message.Role {
		case llm.RoleAssistant:
			for _, call := range message.ToolCalls {
				lines = append(lines, fmt.Sprintf("- 已调用 %s: %s", call.Function.Name, compactLoopText(call.Function.Arguments, 180)))
			}
		case llm.RoleTool:
			lines = append(lines, fmt.Sprintf("  结果 %s: %s", message.Name, compactLoopText(message.Content, 220)))
		}
	}
	if len(lines) == 0 {
		return "- 已完成若干早期检查，详细记录仍保存在任务事件中。"
	}
	return trimLoopTextToTokens(strings.Join(lines, "\n"), maxTokens)
}

func compactLoopText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "..."
}

func trimLoopTextToTokens(value string, maxTokens int) string {
	if maxTokens <= 0 || estimateTextTokens(value) <= maxTokens {
		return value
	}
	runes := []rune(value)
	if len(runes) > maxTokens {
		runes = runes[:maxTokens]
	}
	return string(runes) + "\n- 摘要已截断；不要重复已有检查。"
}

func estimateMessagesTokens(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += 8 + estimateTextTokens(message.Content) + estimateTextTokens(message.Name)
		for _, call := range message.ToolCalls {
			total += 12 + estimateTextTokens(call.Function.Name) + estimateTextTokens(call.Function.Arguments)
		}
	}
	return total
}

func estimateChatRequestTokens(messages []llm.Message, requestTools []llm.Tool) int {
	total := estimateMessagesTokens(messages)
	for _, tool := range requestTools {
		total += 8 + estimateTextTokens(tool.Type)
		total += estimateTextTokens(tool.Function.Name)
		total += estimateTextTokens(tool.Function.Description)
		total += estimateTextTokens(string(tool.Function.Parameters))
	}
	return total
}

func estimateTextTokens(value string) int {
	if value == "" {
		return 0
	}
	asciiRunes := 0
	nonASCIIRunes := 0
	for _, current := range value {
		if current <= 127 {
			asciiRunes++
		} else {
			nonASCIIRunes++
		}
	}
	return (asciiRunes+3)/4 + nonASCIIRunes
}

func toolLoopConvergenceInstruction(round int, maxRounds int) string {
	if maxRounds <= 0 || round < maxRounds/2 {
		return ""
	}
	return fmt.Sprintf(
		"你已经执行了 %d/%d 个模型回合，现在必须收敛。基于已有证据给出结论或实施修复；"+
			"除非能明确说出唯一缺失的关键证据，否则不要继续横向读取文件或变换命令参数翻查同一日志。"+
			"需要用户凭据或输入时直接说明，不要猜测。",
		round,
		maxRounds,
	)
}

func toolLoopFinalInstruction(maxRounds int) string {
	return fmt.Sprintf(
		"这是本轮第 %d 个也是最后一个模型回合，工具已关闭。必须立即给用户完整、诚实的最终答复："+
			"先给结论，再说明已经确认或修改的内容、验证结果，以及仍未完成的事项或所需输入。"+
			"禁止继续说“让我查看”或承诺下一次工具调用，禁止把未验证的猜测写成已完成。",
		maxRounds,
	)
}

func toolLoopGuardFinalInstruction(reason toolLoopInterrupt) string {
	return fmt.Sprintf(
		"工具循环保护已触发：%s 工具现已关闭。必须立即基于已有证据给用户最终答复，不能再说“继续查看”。"+
			"先给出当前最可信的结论，再说明已确认的事实；若缺少用户名、密码、响应体或其他用户输入，明确列出需要用户提供的内容。"+
			"若任务尚未完成，要诚实说明阻塞点和唯一下一步，不得把猜测写成已验证结果。",
		reason.message,
	)
}

func executionPhaseRestartInstruction(phase int, plan taskplan.Plan, ws workspace.Workspace) string {
	return fmt.Sprintf(
		"这是自动续跑的第 %d 个执行阶段。上一阶段没有完成任务；禁止重复浏览历史已读资源。工作区根目录是 `%s`，当前必须只推进以下计划步骤：\n%s\n"+
			"业务服务端口和 API 地址必须从当前工作区的 application.yml、application.properties、Vite 配置、启动脚本或真实启动输出中确认。"+
			"不得把若水自身后端地址当作工作区业务服务地址，也不得用猜测账号反复请求。优先运行工作区已有测试或验收脚本；失败时根据响应体和日志直接修复。",
		phase,
		ws.RootPath,
		renderTaskPlan(plan),
	)
}
