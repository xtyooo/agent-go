package agent

import (
	"context"
	"strings"

	"agentG/internal/runtime/event"
)

type ResultStatus string

const (
	ResultCompleted ResultStatus = "completed"
	ResultPaused    ResultStatus = "paused"
	ResultFailed    ResultStatus = "failed"
)

type Result struct {
	Status       ResultStatus
	Answer       string
	Thinking     string
	References   string
	Recommend    string
	StageOutputs map[string]any
	PauseState   *PauseState
	Error        *ResultError
}

type PauseState struct {
	Name    string
	Content string
	Data    any
}

type ResultError struct {
	Code    string
	Message string
	Detail  string
}

func CallForResult(ctx context.Context, current Agent, input Input) (Result, error) {
	if current == nil {
		return Result{Status: ResultFailed, Error: &ResultError{Message: "agent is nil"}}, nil
	}
	events, err := current.Run(ctx, input)
	if err != nil {
		return Result{Status: ResultFailed, Error: &ResultError{Message: err.Error()}}, err
	}
	return CollectResult(ctx, events), nil
}

func CollectResult(ctx context.Context, events <-chan event.Event) Result {
	var answer strings.Builder
	var thinking strings.Builder
	result := Result{
		Status:       ResultCompleted,
		StageOutputs: map[string]any{},
	}

	for {
		select {
		case <-ctx.Done():
			result.Status = ResultFailed
			result.Error = &ResultError{
				Code:    "CONTEXT_CANCELED",
				Message: ctx.Err().Error(),
				Detail:  ctx.Err().Error(),
			}
			result.Answer = answer.String()
			result.Thinking = thinking.String()
			return result
		case evt, ok := <-events:
			if !ok {
				result.Answer = answer.String()
				result.Thinking = thinking.String()
				return result
			}
			applyEvent(&result, &answer, &thinking, evt)
			if evt.Type == event.TypeComplete || evt.Type == event.TypeError || evt.Type == event.TypePaused {
				result.Answer = answer.String()
				result.Thinking = thinking.String()
				return result
			}
		}
	}
}

func applyEvent(result *Result, answer *strings.Builder, thinking *strings.Builder, evt event.Event) {
	switch evt.Type {
	case event.TypeText:
		answer.WriteString(evt.Content)
	case event.TypeThinking:
		thinking.WriteString(evt.Content)
	case event.TypeReference:
		result.References = evt.Content
	case event.TypeRecommend:
		result.Recommend = evt.Content
		if evt.Data != nil {
			result.StageOutputs["recommend"] = evt.Data
		}
	case event.TypeStageOutput:
		name := firstNonEmpty(evt.Name, evt.Stage, "stage_output")
		if evt.Data != nil {
			result.StageOutputs[name] = evt.Data
		} else {
			result.StageOutputs[name] = evt.Content
		}
	case event.TypePaused:
		result.Status = ResultPaused
		result.PauseState = &PauseState{
			Name:    evt.Name,
			Content: evt.Content,
			Data:    evt.Data,
		}
	case event.TypeError:
		result.Status = ResultFailed
		result.Error = &ResultError{
			Code:    evt.Code,
			Message: firstNonEmpty(evt.Message, evt.Content),
			Detail:  evt.Detail,
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
