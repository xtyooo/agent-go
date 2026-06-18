package react

import (
	"context"

	"agentG/internal/runtime/model"
	"agentG/internal/runtime/tool"
)

type StageTiming string

const (
	StageAfterStart   StageTiming = "after_start"
	StageAfterToolEnd StageTiming = "after_tool_end"
	StageBeforeDone   StageTiming = "before_done"
)

type StageContext struct {
	Params        Params
	Round         int
	Messages      []model.Message
	ToolCall      *model.ToolCall
	ToolResult    *tool.Result
	SearchResults []tool.SearchResult
}

type StageOutputProvider interface {
	Name() string
	Timing() StageTiming
	Produce(ctx context.Context, state StageContext) (any, error)
}

type StageOutputProviderFunc struct {
	ProviderName   string
	ProviderTiming StageTiming
	Fn             func(ctx context.Context, state StageContext) (any, error)
}

func (p StageOutputProviderFunc) Name() string {
	return p.ProviderName
}

func (p StageOutputProviderFunc) Timing() StageTiming {
	return p.ProviderTiming
}

func (p StageOutputProviderFunc) Produce(ctx context.Context, state StageContext) (any, error) {
	if p.Fn == nil {
		return nil, nil
	}
	return p.Fn(ctx, state)
}
