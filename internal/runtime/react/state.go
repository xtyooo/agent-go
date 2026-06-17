package react

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentG/internal/runtime/model"
)

type roundMode string

const (
	roundModeUnknown  roundMode = "unknown"
	roundModeToolCall roundMode = "tool_call"
)

type roundState struct {
	mode       roundMode
	textBuffer strings.Builder
	toolCalls  []model.ToolCall
	inThink    bool
}

func (s roundState) modeForLog() roundMode {
	if s.mode == "" {
		return roundModeUnknown
	}
	return s.mode
}

func (s *roundState) mergeToolCall(incoming model.ToolCall) {
	for i := range s.toolCalls {
		existing := &s.toolCalls[i]
		if sameToolCall(*existing, incoming) {
			if incoming.ID != "" {
				existing.ID = incoming.ID
			}
			if incoming.Name != "" {
				existing.Name = incoming.Name
			}
			existing.Arguments += incoming.Arguments
			return
		}
	}
	s.toolCalls = append(s.toolCalls, incoming)
}

func sameToolCall(left, right model.ToolCall) bool {
	if left.ID != "" && right.ID != "" {
		return left.ID == right.ID
	}
	return left.Index == right.Index
}

type ThinkSegment struct {
	Thinking bool
	Content  string
}

func ParseThinkSegments(chunk string, inThink *bool) []ThinkSegment {
	if chunk == "" {
		return nil
	}

	const startTag = "<think"
	const endTag = "</think"

	var segments []ThinkSegment
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
			segments = append(segments, ThinkSegment{Thinking: currentInThink, Content: chunk[index:]})
			break
		}

		next := start
		isStart := true
		if next < 0 || (end >= 0 && end < next) {
			next = end
			isStart = false
		}

		if next > index {
			segments = append(segments, ThinkSegment{Thinking: currentInThink, Content: chunk[index:next]})
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

func ExtractQuery(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	value, ok := args["query"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func EscapeForJSON(value string) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	trimmed := string(payload)
	return strings.Trim(trimmed, `"`)
}

func ToolArgsSummary(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "invalid_json"
	}
	preferred := []string{"query", "timezone", "city"}
	parts := make([]string, 0, len(preferred))
	for _, key := range preferred {
		if value, ok := args[key]; ok {
			parts = append(parts, key+"="+previewText(fmt.Sprint(value), 60))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ",")
	}
	return fmt.Sprintf("keys=%d", len(args))
}

func ToolResponse(call model.ToolCall, content string) model.Message {
	return model.Message{
		Role:       model.RoleTool,
		Content:    content,
		Name:       call.Name,
		ToolCallID: call.ID,
	}
}

func previewText(value string, limit int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(normalized)) <= limit {
		return normalized
	}
	runes := []rune(normalized)
	return string(runes[:limit]) + "..."
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
