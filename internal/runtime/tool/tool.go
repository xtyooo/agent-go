package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Input struct {
	// Arguments 是模型传来的工具参数，已经从 JSON arguments 解析成 map。
	Arguments map[string]any
}

// Result 是工具执行后的统一返回。
// Content 面向模型继续推理，Data 面向结构化引用/前端展示。
type Result struct {
	// Name 是实际执行的工具名；为空时 Registry.Execute 会自动补齐。
	Name string `json:"name"`
	// Content 是给模型看的文本结果。
	Content string `json:"content"`
	// Data 是给 Agent 收集引用或给前端展示的结构化数据。
	Data map[string]any `json:"data,omitempty"`
}

// Definition 是暴露给模型的工具说明。
type Definition struct {
	// Name 必须全局唯一，并和模型 tool_call.function.name 一致。
	Name string `json:"name"`
	// Description 帮助模型判断工具用途。
	Description string `json:"description"`
	// Schema 是工具参数 JSON Schema。
	Schema map[string]any `json:"schema"`
}

// Tool 是本地工具的最小接口。
// Definition 用于给模型生成 tools schema，Execute 用于真正执行工具。
type Tool interface {
	Definition() Definition
	Execute(ctx context.Context, input Input) (Result, error)
}

// Registry 保存所有可被 Agent 调用的工具。
// 它承担名称去重、按名称查找、schema 导出和统一执行入口。
type Registry struct {
	tools map[string]Tool
}

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool)}
	for _, current := range tools {
		if err := registry.Register(current); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(current Tool) error {
	if current == nil {
		return errors.New("tool is nil")
	}

	def := current.Definition()
	name := strings.TrimSpace(def.Name)
	if name == "" {
		return errors.New("tool name is required")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}

	r.tools[name] = current
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	current, ok := r.tools[name]
	return current, ok
}

func (r *Registry) Definitions() []Definition {
	defs := make([]Definition, 0, len(r.tools))
	for _, current := range r.tools {
		defs = append(defs, current.Definition())
	}

	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	return defs
}

func (r *Registry) Names() []string {
	defs := r.Definitions()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (Result, error) {
	current, ok := r.Get(name)
	if !ok {
		return Result{}, fmt.Errorf("tool %q not found", name)
	}

	result, err := current.Execute(ctx, Input{Arguments: args})
	if err != nil {
		return Result{}, err
	}
	if result.Name == "" {
		result.Name = name
	}

	return result, nil
}

func MustJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func StringArg(args map[string]any, name string) string {
	value, ok := args[name]
	if !ok || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
