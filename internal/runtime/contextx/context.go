package contextx

import (
	"strings"
	"unicode/utf8"

	"agentG/internal/runtime/model"
)

// SectionName 标识 prompt 中的一类上下文来源。
type SectionName string

const (
	SectionSystem  SectionName = "system"
	SectionHistory SectionName = "history"
	SectionCurrent SectionName = "current"
	SectionRAG     SectionName = "rag"
)

// Policy 定义一次模型请求的上下文预算策略。
// 当前使用粗略 token 估算；后续可以替换为模型 tokenizer。
type Policy struct {
	// MaxInputTokens 是本次模型输入最多允许的估算 token 数。
	MaxInputTokens int
	// ReservedOutputTokens 是给模型输出预留的 token 数，仅用于日志和预算说明。
	ReservedOutputTokens int
	// MaxHistoryTokens 是历史消息最多允许占用的估算 token 数。
	MaxHistoryTokens int
	// CharsPerToken 是字符到 token 的粗略换算比例。
	CharsPerToken int
}

// DefaultPolicy 返回保守默认值。
func DefaultPolicy() Policy {
	return Policy{
		MaxInputTokens:       12000,
		ReservedOutputTokens: 2000,
		MaxHistoryTokens:     4000,
		CharsPerToken:        4,
	}
}

func (p Policy) Normalize() Policy {
	if p.MaxInputTokens <= 0 {
		p.MaxInputTokens = 12000
	}
	if p.ReservedOutputTokens < 0 {
		p.ReservedOutputTokens = 0
	}
	if p.MaxHistoryTokens <= 0 {
		p.MaxHistoryTokens = 4000
	}
	if p.CharsPerToken <= 0 {
		p.CharsPerToken = 4
	}
	if p.MaxHistoryTokens > p.MaxInputTokens {
		p.MaxHistoryTokens = p.MaxInputTokens
	}
	return p
}

// Section 是 prompt 组成的一部分。
type Section struct {
	Name     SectionName
	Messages []model.Message
}

// BuildResult 是应用上下文策略后的结果。
type BuildResult struct {
	Messages []model.Message
	Summary  Summary
}

// Summary 用于日志解释本次 prompt 由哪些部分组成、裁剪了什么。
type Summary struct {
	Policy                 Policy
	InputTokenEstimate     int
	SystemTokenEstimate    int
	HistoryTokenEstimate   int
	CurrentTokenEstimate   int
	RAGTokenEstimate       int
	HistoryMessageInput    int
	HistoryMessageKept     int
	HistoryMessageDropped  int
	HistoryTokenBeforeTrim int
}

// Builder 根据 Policy 组装和裁剪模型上下文。
type Builder struct {
	policy Policy
}

func NewBuilder(policy Policy) *Builder {
	return &Builder{policy: policy.Normalize()}
}

// Build 按 section 组装上下文。
// 当前策略只裁剪 history：优先保留越新的历史消息，system/current 不裁剪。
func (b *Builder) Build(sections ...Section) BuildResult {
	policy := b.policy.Normalize()
	result := BuildResult{
		Summary: Summary{Policy: policy},
	}

	var systemMessages []model.Message
	var historyMessages []model.Message
	var currentMessages []model.Message
	var ragMessages []model.Message

	for _, section := range sections {
		switch section.Name {
		case SectionSystem:
			systemMessages = append(systemMessages, section.Messages...)
		case SectionHistory:
			historyMessages = append(historyMessages, section.Messages...)
		case SectionCurrent:
			currentMessages = append(currentMessages, section.Messages...)
		case SectionRAG:
			ragMessages = append(ragMessages, section.Messages...)
		}
	}

	result.Summary.SystemTokenEstimate = EstimateMessages(systemMessages, policy)
	result.Summary.CurrentTokenEstimate = EstimateMessages(currentMessages, policy)
	result.Summary.RAGTokenEstimate = EstimateMessages(ragMessages, policy)
	result.Summary.HistoryMessageInput = len(historyMessages)
	result.Summary.HistoryTokenBeforeTrim = EstimateMessages(historyMessages, policy)

	availableInput := policy.MaxInputTokens - result.Summary.SystemTokenEstimate - result.Summary.CurrentTokenEstimate - result.Summary.RAGTokenEstimate
	if availableInput < 0 {
		availableInput = 0
	}
	historyBudget := minInt(policy.MaxHistoryTokens, availableInput)
	trimmedHistory := TrimHistory(historyMessages, historyBudget, policy)

	result.Summary.HistoryMessageKept = len(trimmedHistory)
	result.Summary.HistoryMessageDropped = len(historyMessages) - len(trimmedHistory)
	result.Summary.HistoryTokenEstimate = EstimateMessages(trimmedHistory, policy)

	result.Messages = make([]model.Message, 0, len(systemMessages)+len(trimmedHistory)+len(ragMessages)+len(currentMessages))
	result.Messages = append(result.Messages, systemMessages...)
	if len(trimmedHistory) > 0 {
		result.Messages = append(result.Messages, model.Message{Role: model.RoleUser, Content: "对话历史："})
		result.Messages = append(result.Messages, trimmedHistory...)
	}
	result.Messages = append(result.Messages, ragMessages...)
	result.Messages = append(result.Messages, currentMessages...)

	result.Summary.InputTokenEstimate = EstimateMessages(result.Messages, policy)
	return result
}

// TrimHistory 保留最近的历史消息，并确保不超过预算。
func TrimHistory(messages []model.Message, maxTokens int, policy Policy) []model.Message {
	if maxTokens <= 0 || len(messages) == 0 {
		return nil
	}

	policy = policy.Normalize()
	var reversed []model.Message
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		cost := EstimateMessage(messages[i], policy)
		if used+cost > maxTokens && len(reversed) > 0 {
			break
		}
		if cost > maxTokens && len(reversed) == 0 {
			break
		}
		used += cost
		reversed = append(reversed, messages[i])
	}

	out := make([]model.Message, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		out = append(out, reversed[i])
	}
	return out
}

func EstimateMessages(messages []model.Message, policy Policy) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessage(msg, policy)
	}
	return total
}

func EstimateMessage(msg model.Message, policy Policy) int {
	policy = policy.Normalize()
	chars := utf8.RuneCountInString(string(msg.Role)) + utf8.RuneCountInString(msg.Content)
	chars += utf8.RuneCountInString(msg.Name) + utf8.RuneCountInString(msg.ToolCallID)
	for _, call := range msg.ToolCalls {
		chars += utf8.RuneCountInString(call.ID)
		chars += utf8.RuneCountInString(call.Name)
		chars += utf8.RuneCountInString(call.Arguments)
	}
	return chars/policy.CharsPerToken + 4
}

func PreviewMessages(messages []model.Message, limit int) string {
	var b strings.Builder
	for _, msg := range messages {
		if b.Len() > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(string(msg.Role))
		b.WriteString(":")
		b.WriteString(strings.TrimSpace(msg.Content))
		if limit > 0 && utf8.RuneCountInString(b.String()) >= limit {
			runes := []rune(b.String())
			return string(runes[:limit]) + "..."
		}
	}
	return b.String()
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
