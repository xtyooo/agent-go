package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"agentG/internal/runtime/tool"
)

const (
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
	TransportCommand        = "command"
)

// Config 是 MCP Runtime 的启动配置。它只描述外部 MCP server，不直接暴露给 Agent。
type Config struct {
	Servers []ServerConfig
}

// ServerConfig 描述一个 MCP server 的连接参数和工具注册策略。
type ServerConfig struct {
	Name                 string
	Enabled              bool
	Transport            string
	URL                  string
	Command              string
	Args                 []string
	Headers              map[string]string
	ToolPrefix           string
	Timeout              time.Duration
	DisableStandaloneSSE bool
}

// Loaded 记录本次启动成功加载的 MCP session 和工具。
// main.go 需要 defer Close，避免 command/stdout 或 streamable HTTP SSE 连接残留。
type Loaded struct {
	Tools    []tool.Tool
	Sessions []*Session
}

func (l Loaded) Close() error {
	var joined error
	for _, session := range l.Sessions {
		if session == nil {
			continue
		}
		if err := session.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// LoadTools 连接所有已启用的 MCP server，把远端 tools/list 结果适配成现有 tool.Tool。
func LoadTools(ctx context.Context, cfg Config, logger *slog.Logger) (Loaded, error) {
	if logger == nil {
		logger = slog.Default()
	}

	loaded := Loaded{}
	enabledCount := 0
	for _, server := range cfg.Servers {
		if !server.Enabled {
			continue
		}
		enabledCount++
		current, err := loadServer(ctx, server, logger)
		if err != nil {
			_ = loaded.Close()
			return Loaded{}, err
		}
		loaded.Tools = append(loaded.Tools, current.Tools...)
		loaded.Sessions = append(loaded.Sessions, current.Sessions...)
	}

	logger.Info("🧩 MCP Runtime 加载完成",
		"enabled_server_count", enabledCount,
		"session_count", len(loaded.Sessions),
		"tool_count", len(loaded.Tools),
	)
	return loaded, nil
}

func loadServer(ctx context.Context, cfg ServerConfig, logger *slog.Logger) (Loaded, error) {
	cfg = normalizeServerConfig(cfg)
	startedAt := time.Now()
	logger.Info("🧩 MCP Server 开始连接",
		"server", cfg.Name,
		"transport", cfg.Transport,
		"url", cfg.URL,
		"command", cfg.Command,
		"timeout_ms", cfg.Timeout.Milliseconds(),
	)

	if err := validateServerConfig(cfg); err != nil {
		return Loaded{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	client := sdk.NewClient(&sdk.Implementation{
		Name:    "KimoAgent",
		Version: "go-mcp-runtime",
	}, &sdk.ClientOptions{Logger: logger.With("mcp_server", cfg.Name)})

	session, err := client.Connect(ctx, transportFor(cfg), nil)
	if err != nil {
		logger.Error("❌ MCP Server 连接失败",
			"server", cfg.Name,
			"transport", cfg.Transport,
			"elapsed_ms", time.Since(startedAt).Milliseconds(),
			"error", err,
		)
		return Loaded{}, fmt.Errorf("connect mcp server %q: %w", cfg.Name, err)
	}

	wrapped := &Session{
		ServerName: cfg.Name,
		session:    session,
		timeout:    cfg.Timeout,
		logger:     logger,
	}

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = wrapped.Close()
		logger.Error("❌ MCP Server 工具列表读取失败",
			"server", cfg.Name,
			"elapsed_ms", time.Since(startedAt).Milliseconds(),
			"error", err,
		)
		return Loaded{}, fmt.Errorf("list mcp tools for server %q: %w", cfg.Name, err)
	}

	tools := make([]tool.Tool, 0, len(result.Tools))
	for _, remote := range result.Tools {
		if remote == nil {
			continue
		}
		adapter, err := NewToolAdapter(wrapped, cfg, RemoteTool{
			Name:        remote.Name,
			Title:       remote.Title,
			Description: remote.Description,
			InputSchema: remote.InputSchema,
		})
		if err != nil {
			_ = wrapped.Close()
			return Loaded{}, err
		}
		tools = append(tools, adapter)
	}

	logger.Info("✅ MCP Server 工具已加载",
		"server", cfg.Name,
		"tool_count", len(tools),
		"tool_prefix", effectiveToolPrefix(cfg),
		"elapsed_ms", time.Since(startedAt).Milliseconds(),
	)
	return Loaded{Tools: tools, Sessions: []*Session{wrapped}}, nil
}

func normalizeServerConfig(cfg ServerConfig) ServerConfig {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Transport = strings.ToLower(strings.TrimSpace(cfg.Transport))
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.ToolPrefix = strings.TrimSpace(cfg.ToolPrefix)
	if cfg.Transport == "" {
		cfg.Transport = TransportStreamableHTTP
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Headers == nil {
		cfg.Headers = map[string]string{}
	}
	return cfg
}

func validateServerConfig(cfg ServerConfig) error {
	if cfg.Name == "" {
		return errors.New("mcp server name is required")
	}
	switch cfg.Transport {
	case TransportStreamableHTTP, TransportSSE:
		if cfg.URL == "" {
			return fmt.Errorf("mcp server %q url is required for %s transport", cfg.Name, cfg.Transport)
		}
	case TransportCommand:
		if cfg.Command == "" {
			return fmt.Errorf("mcp server %q command is required for command transport", cfg.Name)
		}
	default:
		return fmt.Errorf("mcp server %q transport %q is not supported", cfg.Name, cfg.Transport)
	}
	return nil
}

func transportFor(cfg ServerConfig) sdk.Transport {
	switch cfg.Transport {
	case TransportSSE:
		return &sdk.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient(cfg.Headers),
		}
	case TransportCommand:
		return &sdk.CommandTransport{
			Command: exec.Command(cfg.Command, cfg.Args...),
		}
	default:
		return &sdk.StreamableClientTransport{
			Endpoint:             cfg.URL,
			HTTPClient:           httpClient(cfg.Headers),
			DisableStandaloneSSE: cfg.DisableStandaloneSSE,
		}
	}
}

func httpClient(headers map[string]string) *http.Client {
	return &http.Client{
		Transport: headerTransport{
			base:    http.DefaultTransport,
			headers: headers,
		},
	}
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if len(t.headers) == 0 {
		return base.RoundTrip(req)
	}

	cloned := req.Clone(req.Context())
	for key, value := range t.headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		cloned.Header.Set(key, value)
	}
	return base.RoundTrip(cloned)
}

// Session 是官方 MCP ClientSession 的窄接口包装，便于工具适配器测试。
type Session struct {
	ServerName string
	session    *sdk.ClientSession
	timeout    time.Duration
	logger     *slog.Logger
}

func (s *Session) CallTool(ctx context.Context, name string, arguments map[string]any) (*CallResult, error) {
	if s == nil || s.session == nil {
		return nil, errors.New("mcp session is not initialized")
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := s.session.CallTool(ctx, &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return nil, err
	}
	return fromSDKCallResult(result), nil
}

func (s *Session) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close()
}

type caller interface {
	CallTool(ctx context.Context, name string, arguments map[string]any) (*CallResult, error)
}

// RemoteTool 是 MCP tools/list 返回值在本地的稳定表示。
type RemoteTool struct {
	Name        string
	Title       string
	Description string
	InputSchema any
}

// CallResult 是 MCP tools/call 返回值在本地的稳定表示。
type CallResult struct {
	Content           string
	StructuredContent any
	IsError           bool
}

func fromSDKCallResult(result *sdk.CallToolResult) *CallResult {
	if result == nil {
		return &CallResult{}
	}
	return &CallResult{
		Content:           contentText(result.Content),
		StructuredContent: result.StructuredContent,
		IsError:           result.IsError,
	}
}

func contentText(contents []sdk.Content) string {
	parts := make([]string, 0, len(contents))
	for _, content := range contents {
		switch typed := content.(type) {
		case *sdk.TextContent:
			if strings.TrimSpace(typed.Text) != "" {
				parts = append(parts, typed.Text)
			}
		default:
			payload, err := json.Marshal(typed)
			if err == nil && len(payload) > 0 {
				parts = append(parts, string(payload))
			}
		}
	}
	return strings.Join(parts, "\n")
}
