package planexecute

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/model"
	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

// Task 是 Plan 阶段输出的最小执行单元。
// Java dodo-agent 中对应 PlanTask：id 用于追踪任务，instruction 是给执行器的任务描述，order 表示依赖顺序。
type Task struct {
	// ID 为空表示 planner 判断“无需继续检索，可以进入总结”。
	ID string `json:"id"`
	// Instruction 是具体检索任务。当前 Go 版先统一交给 web_search 执行。
	Instruction string `json:"instruction"`
	// Order 表示执行顺序；order 越小越先执行。当前先串行执行，后续可按相同 order 并行。
	Order int `json:"order"`
}

// TaskResult 保存单个工具任务执行后的结果。
// 它会进入 runState，供 Critique 和 Summarize 阶段继续使用。
type TaskResult struct {
	// Task 是原始计划任务。
	Task Task
	// Tool 是实际执行的工具名称。
	Tool string
	// Query 是传给工具的检索词/任务文本。
	Query string
	// Result 是工具返回的文本或结构化 JSON。
	Result string
	// Error 非空表示工具执行失败；失败结果也会进入上下文，让模型能解释不确定性。
	Error string
}

// Critique 是评审阶段的结构化输出。
type Critique struct {
	// Passed=true 表示当前材料足够进入最终总结。
	Passed bool `json:"passed"`
	// Feedback 是未通过时下一轮 Plan 必须优先补齐的研究缺口。
	Feedback string `json:"feedback"`
}

// runState 是一次 Plan-Execute Run 的工作记忆。
// 它对应 Java OverAllState 的简化版：保留原始问题、历史、研究主题、执行结果和评审反馈。
type runState struct {
	// question 是用户原始问题，最终报告必须围绕它回答。
	question string
	// historyMessages 是 Memory Runtime 加载并经 Context Runtime 裁剪后的历史问答。
	historyMessages []model.Message
	// refinedResearchTopic 是需求澄清后生成的研究分析点，帮助后续 Plan 聚焦。
	refinedResearchTopic string
	// round 是当前 Plan-Execute 研究轮次。
	round int
	// results 保存所有已执行任务结果，供后续计划、评审和最终总结复用。
	results []TaskResult
	// references 聚合 web_search 返回的结构化引用，最终作为 reference 事件输出。
	references []tool.SearchResult
	// critiquePassed 保存最近一次评审是否通过，主要用于日志和上下文解释。
	critiquePassed bool
	// critiqueFeedback 保存最近一次未通过的原因，下一轮 Plan 必须优先处理。
	critiqueFeedback string
}

func (s *runState) researchContext() string {
	var b strings.Builder
	b.WriteString("【User Question】\n")
	b.WriteString(s.question)
	b.WriteString("\n\n")
	if strings.TrimSpace(s.refinedResearchTopic) != "" {
		b.WriteString("【Refined Research Topic】\n")
		b.WriteString(s.refinedResearchTopic)
		b.WriteString("\n\n")
	}
	if len(s.historyMessages) > 0 {
		b.WriteString("【Conversation History】\n")
		for _, msg := range s.historyMessages {
			b.WriteString("- ")
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(previewText(msg.Content, 800))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if s.critiqueFeedback != "" {
		b.WriteString("【Critique Feedback】\n")
		b.WriteString(s.critiqueFeedback)
		b.WriteString("\n\n")
	}
	b.WriteString("【Executed Tasks】\n")
	if len(s.results) == 0 {
		b.WriteString("NONE\n")
		return b.String()
	}
	for _, result := range s.results {
		b.WriteString("- Task ID: ")
		b.WriteString(result.Task.ID)
		b.WriteString("\n  Instruction: ")
		b.WriteString(result.Task.Instruction)
		b.WriteString("\n  Tool: ")
		b.WriteString(result.Tool)
		b.WriteString("\n  Query: ")
		b.WriteString(result.Query)
		if result.Error != "" {
			b.WriteString("\n  Error: ")
			b.WriteString(result.Error)
		}
		b.WriteString("\n  Result: ")
		b.WriteString(previewText(result.Result, 2400))
		b.WriteString("\n")
	}
	return b.String()
}

func parseTasks(content string) ([]Task, error) {
	raw := extractJSON(content)
	var tasks []Task
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func parseCritique(content string) (Critique, error) {
	raw := extractJSON(content)
	var critique Critique
	if err := json.Unmarshal([]byte(raw), &critique); err != nil {
		return Critique{}, err
	}
	return critique, nil
}

func extractJSON(content string) string {
	trimmed := strings.TrimSpace(stripThinkTags(content))
	if strings.HasPrefix(trimmed, "```") {
		re := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
		if match := re.FindStringSubmatch(trimmed); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	startArray := strings.Index(trimmed, "[")
	endArray := strings.LastIndex(trimmed, "]")
	if startArray >= 0 && endArray > startArray {
		return trimmed[startArray : endArray+1]
	}
	startObject := strings.Index(trimmed, "{")
	endObject := strings.LastIndex(trimmed, "}")
	if startObject >= 0 && endObject > startObject {
		return trimmed[startObject : endObject+1]
	}
	return trimmed
}

func planDone(tasks []Task) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.ID) != "" {
			return false
		}
	}
	return true
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func escapeForJSON(value string) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return strings.Trim(string(payload), `"`)
}

func previewText(value string, limit int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(normalized)) <= limit {
		return normalized
	}
	runes := []rune(normalized)
	return string(runes[:limit]) + "..."
}

type thinkSegment struct {
	// thinking=true 表示片段来自 <think> 标签内部，应作为 thinking 事件输出。
	thinking bool
	// content 是已经剥离 think 标签后的文本片段。
	content string
}

// parseThinkSegments 把模型流式文本按 <think> 标签切分为 thinking/text。
// inThink 由调用方持有，因为开始和结束标签可能落在不同 chunk 中。
func parseThinkSegments(chunk string, inThink *bool) []thinkSegment {
	if chunk == "" {
		return nil
	}

	const startTag = "<think"
	const endTag = "</think"

	var segments []thinkSegment
	currentInThink := *inThink
	index := 0
	for index < len(chunk) {
		start := strings.Index(chunk[index:], startTag)
		end := strings.Index(chunk[index:], endTag)
		if start >= 0 {
			start += index
		}
		if end >= 0 {
			end += index
		}

		if start < 0 && end < 0 {
			segments = append(segments, thinkSegment{thinking: currentInThink, content: chunk[index:]})
			break
		}

		next := start
		isStart := true
		if next < 0 || (end >= 0 && end < next) {
			next = end
			isStart = false
		}
		if next > index {
			segments = append(segments, thinkSegment{thinking: currentInThink, content: chunk[index:next]})
		}

		tagEnd := strings.Index(chunk[next:], ">")
		if tagEnd < 0 {
			currentInThink = isStart
			break
		}
		currentInThink = isStart
		index = next + tagEnd + 1
	}

	*inThink = currentInThink
	return segments
}

func stripThinkTags(content string) string {
	var b strings.Builder
	inThink := false
	for _, segment := range parseThinkSegments(content, &inThink) {
		if !segment.thinking {
			b.WriteString(segment.content)
		}
	}
	return b.String()
}

func searchResultsFromAny(value any) []tool.SearchResult {
	switch typed := value.(type) {
	case []tool.SearchResult:
		return typed
	case []any:
		results := make([]tool.SearchResult, 0, len(typed))
		for _, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			results = append(results, tool.SearchResult{
				Title:   fmt.Sprint(itemMap["title"]),
				URL:     fmt.Sprint(itemMap["url"]),
				Content: fmt.Sprint(itemMap["content"]),
			})
		}
		return results
	default:
		return nil
	}
}

// runRecord 保存一次 Plan-Execute Run 的可持久化结果。
// Java 版在 sink.doOnNext/doFinally 里收集这些字段；Go 版统一在 send 事件出口收集。
type runRecord struct {
	conversationID  string
	question        string
	sessionRecordID int64
	startedAt       time.Time
	answer          strings.Builder
	thinking        strings.Builder
	reference       string
	recommend       string
	firstResponseMs int64
	tools           map[string]struct{}
}

func newRunRecord(conversationID, question string, startedAt time.Time) *runRecord {
	return &runRecord{
		conversationID:  conversationID,
		question:        question,
		startedAt:       startedAt,
		firstResponseMs: -1,
		tools:           make(map[string]struct{}),
	}
}

func (r *runRecord) capture(evt event.Event, elapsedMs int64) {
	if r == nil {
		return
	}
	if r.firstResponseMs < 0 && (evt.Type == event.TypeText || evt.Type == event.TypeThinking || evt.Type == event.TypeToolStart || evt.Type == event.TypeReference) {
		r.firstResponseMs = elapsedMs
	}

	switch evt.Type {
	case event.TypeText:
		r.answer.WriteString(evt.Content)
	case event.TypeThinking:
		r.thinking.WriteString(evt.Content)
	case event.TypeToolStart, event.TypeToolEnd:
		if evt.ToolName != "" {
			r.tools[evt.ToolName] = struct{}{}
		}
	case event.TypeReference:
		r.reference = evt.Content
	case event.TypeRecommend:
		r.recommend = evt.Content
	}
}

func (r *runRecord) toolsString() string {
	if r == nil || len(r.tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
