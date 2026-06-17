package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type TimeTool struct {
	now func() time.Time
}

func NewTimeTool() *TimeTool {
	return &TimeTool{now: time.Now}
}

func (t *TimeTool) Definition() Definition {
	return Definition{
		Name:        "current_time",
		Description: "Return the current server time for a requested timezone offset.",
		Schema: objectSchema(map[string]any{
			"timezone": map[string]any{
				"type":        "string",
				"description": "Timezone label or UTC offset, for example Asia/Shanghai or +08:00.",
			},
		}, []string{}),
	}
}

func (t *TimeTool) Execute(ctx context.Context, input Input) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	timezone := StringArg(input.Arguments, "timezone")
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}

	now := t.now()
	return Result{
		Name:    "current_time",
		Content: fmt.Sprintf("Current server time is %s, timezone=%s.", now.Format(time.RFC3339), timezone),
		Data: map[string]any{
			"time":     now.Format(time.RFC3339),
			"timezone": timezone,
		},
	}, nil
}

type WeatherMockTool struct{}

func NewWeatherMockTool() *WeatherMockTool {
	return &WeatherMockTool{}
}

func (t *WeatherMockTool) Definition() Definition {
	return Definition{
		Name:        "weather_mock",
		Description: "Return deterministic mock weather for learning the tool runtime.",
		Schema: objectSchema(map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "City name, for example Shanghai.",
			},
		}, []string{"city"}),
	}
}

func (t *WeatherMockTool) Execute(ctx context.Context, input Input) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	city := StringArg(input.Arguments, "city")
	if city == "" {
		return Result{}, fmt.Errorf("city is required")
	}

	return Result{
		Name:    "weather_mock",
		Content: fmt.Sprintf("%s mock weather: cloudy, 26C, southeast wind level 2.", city),
		Data: map[string]any{
			"city":        city,
			"condition":   "cloudy",
			"temperature": 26,
			"wind":        "southeast wind level 2",
			"mock":        true,
		},
	}, nil
}

type WebSearchMockTool struct{}

func NewWebSearchMockTool() *WebSearchMockTool {
	return &WebSearchMockTool{}
}

func (t *WebSearchMockTool) Definition() Definition {
	return Definition{
		Name:        "web_search_mock",
		Description: "Return mock search snippets so the agent can expose tool events before real search is added.",
		Schema:      searchSchema(),
	}
}

func (t *WebSearchMockTool) Execute(ctx context.Context, input Input) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	query := StringArg(input.Arguments, "query")
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}

	snippets := []string{
		fmt.Sprintf("Mock result 1: core concepts and background for %q.", query),
		fmt.Sprintf("Mock result 2: implementation path and caveats for %q.", query),
		fmt.Sprintf("Mock result 3: follow-up reading and validation methods for %q.", query),
	}
	results := []SearchResult{
		{
			Title:   "Mock search result: concepts",
			URL:     "https://example.com/mock/concepts",
			Content: snippets[0],
		},
		{
			Title:   "Mock search result: implementation",
			URL:     "https://example.com/mock/implementation",
			Content: snippets[1],
		},
		{
			Title:   "Mock search result: validation",
			URL:     "https://example.com/mock/validation",
			Content: snippets[2],
		},
	}

	return Result{
		Name:    "web_search_mock",
		Content: strings.Join(snippets, "\n"),
		Data: map[string]any{
			"query":    query,
			"snippets": snippets,
			"results":  results,
			"mock":     true,
		},
	}, nil
}

// DefaultRegistryConfig 是默认工具集的外部配置。
// 当前只有 web_search 需要访问外部服务，后续新增工具可以继续扩展这里。
type DefaultRegistryConfig struct {
	WebSearch  WebSearchConfig
	ReadSkill  Tool
	ExtraTools []Tool
}

func NewDefaultRegistry(cfg DefaultRegistryConfig) (*Registry, error) {
	tools := []Tool{
		NewTimeTool(),
		NewWeatherMockTool(),
		NewWebSearchTool(cfg.WebSearch),
		NewWebSearchMockTool(),
	}
	if cfg.ReadSkill != nil {
		tools = append(tools, cfg.ReadSkill)
	}
	tools = append(tools, cfg.ExtraTools...)
	return NewRegistry(tools...)
}

func searchSchema() map[string]any {
	return objectSchema(map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "Search query.",
		},
	}, []string{"query"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
