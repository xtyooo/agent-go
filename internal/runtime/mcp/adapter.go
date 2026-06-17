package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

var invalidToolNameChar = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ToolAdapter 把一个远端 MCP tool 包装成本项目已有的 tool.Tool。
// 这层对应 Java dodo-agent 中 SyncMcpToolCallbackProvider 产出的 ToolCallback。
type ToolAdapter struct {
	caller      caller
	localName   string
	remoteName  string
	serverName  string
	description string
	schema      map[string]any
	logger      *slog.Logger
}

func NewToolAdapter(c caller, cfg ServerConfig, remote RemoteTool) (*ToolAdapter, error) {
	cfg = normalizeServerConfig(cfg)
	if c == nil {
		return nil, errors.New("mcp tool caller is required")
	}
	if cfg.Name == "" {
		return nil, errors.New("mcp server name is required")
	}
	remote.Name = strings.TrimSpace(remote.Name)
	if remote.Name == "" {
		return nil, fmt.Errorf("mcp server %q returned a tool without name", cfg.Name)
	}

	localName := localToolName(cfg, remote.Name)
	if localName == "" {
		return nil, fmt.Errorf("mcp tool %q from server %q cannot be converted to a local tool name", remote.Name, cfg.Name)
	}

	description := strings.TrimSpace(remote.Description)
	if description == "" {
		description = strings.TrimSpace(remote.Title)
	}
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s.", remote.Name, cfg.Name)
	}

	return &ToolAdapter{
		caller:      c,
		localName:   localName,
		remoteName:  remote.Name,
		serverName:  cfg.Name,
		description: fmt.Sprintf("%s\n\n远端 MCP server: %s；远端工具名: %s。", description, cfg.Name, remote.Name),
		schema:      normalizeInputSchema(remote.InputSchema),
	}, nil
}

func (t *ToolAdapter) Definition() tool.Definition {
	return tool.Definition{
		Name:        t.localName,
		Description: t.description,
		Schema:      t.schema,
	}
}

func (t *ToolAdapter) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	startedAt := time.Now()
	logger := t.log()
	logger.Info("🔌 MCP 工具调用开始",
		"server", t.serverName,
		"tool", t.localName,
		"remote_tool", t.remoteName,
		"args_chars", len(tool.MustJSON(input.Arguments)),
	)

	result, err := t.caller.CallTool(ctx, t.remoteName, input.Arguments)
	if err != nil {
		logger.Error("❌ MCP 工具调用失败",
			"server", t.serverName,
			"tool", t.localName,
			"remote_tool", t.remoteName,
			"elapsed_ms", time.Since(startedAt).Milliseconds(),
			"error", err,
		)
		return tool.Result{}, err
	}

	content := strings.TrimSpace(result.Content)
	if content == "" && result.StructuredContent != nil {
		content = tool.MustJSON(result.StructuredContent)
	}
	if content == "" {
		content = "{}"
	}

	data := map[string]any{
		"provider":    "mcp",
		"server":      t.serverName,
		"remote_tool": t.remoteName,
		"is_error":    result.IsError,
	}
	if result.StructuredContent != nil {
		data["structured_content"] = result.StructuredContent
	}

	logger.Info("✅ MCP 工具调用完成",
		"server", t.serverName,
		"tool", t.localName,
		"remote_tool", t.remoteName,
		"content_chars", len(content),
		"is_error", result.IsError,
		"elapsed_ms", time.Since(startedAt).Milliseconds(),
	)
	return tool.Result{
		Name:    t.localName,
		Content: content,
		Data:    data,
	}, nil
}

func (t *ToolAdapter) log() *slog.Logger {
	if t.logger != nil {
		return t.logger
	}
	return slog.Default()
}

func localToolName(cfg ServerConfig, remoteName string) string {
	remote := sanitizeToolName(remoteName)
	prefix := effectiveToolPrefix(cfg)
	if prefix == "" {
		return remote
	}
	return sanitizeToolName(prefix + "_" + remote)
}

func effectiveToolPrefix(cfg ServerConfig) string {
	if strings.TrimSpace(cfg.ToolPrefix) == "-" {
		return ""
	}
	if strings.TrimSpace(cfg.ToolPrefix) != "" {
		return sanitizeToolName(cfg.ToolPrefix)
	}
	return "mcp_" + sanitizeToolName(cfg.Name)
}

func sanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	name = invalidToolNameChar.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_-")
	return name
}

func normalizeInputSchema(input any) map[string]any {
	schema := map[string]any{}
	if input != nil {
		payload, err := json.Marshal(input)
		if err == nil {
			_ = json.Unmarshal(payload, &schema)
		}
	}

	if len(schema) == 0 {
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": true,
		}
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	if _, ok := schema["properties"]; !ok {
		schema["properties"] = map[string]any{}
	}
	if _, ok := schema["required"]; !ok {
		schema["required"] = []string{}
	}
	return schema
}

var _ tool.Tool = (*ToolAdapter)(nil)
