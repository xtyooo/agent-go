package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

type fakeCaller struct {
	name string
	args map[string]any
	res  *CallResult
	err  error
}

func (f *fakeCaller) CallTool(_ context.Context, name string, arguments map[string]any) (*CallResult, error) {
	f.name = name
	f.args = arguments
	return f.res, f.err
}

func TestToolAdapterDefinitionAndExecute(t *testing.T) {
	caller := &fakeCaller{
		res: &CallResult{
			Content:           "search result",
			StructuredContent: map[string]any{"answer": "ok"},
		},
	}
	adapter, err := NewToolAdapter(caller, ServerConfig{
		Name:       "tavily",
		ToolPrefix: "mcp_tavily",
		Timeout:    time.Second,
	}, RemoteTool{
		Name:        "web.search",
		Description: "Search web",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	def := adapter.Definition()
	if def.Name != "mcp_tavily_web_search" {
		t.Fatalf("unexpected local tool name: %s", def.Name)
	}
	if !strings.Contains(def.Description, "远端 MCP server: tavily") {
		t.Fatalf("description should mention remote server: %s", def.Description)
	}
	if def.Schema["type"] != "object" {
		t.Fatalf("unexpected schema: %#v", def.Schema)
	}

	result, err := adapter.Execute(context.Background(), tool.Input{Arguments: map[string]any{"query": "golang mcp"}})
	if err != nil {
		t.Fatalf("execute adapter: %v", err)
	}
	if caller.name != "web.search" {
		t.Fatalf("expected remote tool name, got %s", caller.name)
	}
	if caller.args["query"] != "golang mcp" {
		t.Fatalf("unexpected args: %#v", caller.args)
	}
	if result.Name != "mcp_tavily_web_search" || result.Content != "search result" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Data["provider"] != "mcp" || result.Data["remote_tool"] != "web.search" {
		t.Fatalf("unexpected result data: %#v", result.Data)
	}
}

func TestToolAdapterCanDisablePrefix(t *testing.T) {
	adapter, err := NewToolAdapter(&fakeCaller{res: &CallResult{}}, ServerConfig{
		Name:       "local",
		ToolPrefix: "-",
	}, RemoteTool{Name: "echo"})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	if got := adapter.Definition().Name; got != "echo" {
		t.Fatalf("expected remote name without prefix, got %s", got)
	}
}

func TestNormalizeInputSchemaFallback(t *testing.T) {
	schema := normalizeInputSchema(nil)
	if schema["type"] != "object" {
		t.Fatalf("expected object schema: %#v", schema)
	}
	if schema["properties"] == nil {
		t.Fatalf("expected properties fallback: %#v", schema)
	}
}
