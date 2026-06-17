package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是应用启动配置，对应项目根目录的 application.yaml。
// 它只描述外部可变参数，业务运行逻辑仍然放在 model/tool/agent 各自模块中。
type Config struct {
	// Server 保存 HTTP 服务配置。
	Server ServerConfig `yaml:"server"`
	// Model 保存 OpenAI-compatible 模型平台配置。
	Model ModelConfig `yaml:"model"`
	// Agent 保存 Agent 运行策略配置。
	Agent AgentConfig `yaml:"agent"`
	// Memory 保存会话记忆配置。
	Memory MemoryConfig `yaml:"memory"`
	// Context 保存模型上下文预算和裁剪策略配置。
	Context ContextConfig `yaml:"context"`
	// Tools 保存本地工具所需的外部服务配置。
	Tools ToolsConfig `yaml:"tools"`
	// Skills 保存本地技能目录配置，对应 Java dodo-agent 的 skills.directory。
	Skills SkillsConfig `yaml:"skills"`
	// MCP 保存外部 MCP Server 配置，对应 Java dodo-agent 的 McpSyncClient 初始化。
	MCP MCPConfig `yaml:"mcp"`
	// Observability 保存可观测性配置，例如 trace 记录和离线回放。
	Observability ObservabilityConfig `yaml:"observability"`
}

// ServerConfig 是 HTTP Server 的启动参数。
type ServerConfig struct {
	// Port 是监听端口，不带冒号，例如 "8888"。
	Port string `yaml:"port"`
}

// ModelConfig 是模型运行时配置。
type ModelConfig struct {
	// BaseURL 是 OpenAI-compatible 服务地址，不包含 /v1/chat/completions。
	BaseURL string `yaml:"base-url"`
	// APIKey 是模型平台密钥。
	APIKey string `yaml:"api-key"`
	// Name 是模型名称，例如 qwen3.6-flash-2026-04-16。
	Name string `yaml:"name"`
}

// AgentConfig 是 Agent 本身的策略参数。
type AgentConfig struct {
	// MaxRounds 限制 ReAct 最大推理轮数，避免工具调用无限循环。
	MaxRounds int `yaml:"max-rounds"`
}

// MemoryConfig 是会话记忆运行时配置。
type MemoryConfig struct {
	// Enabled 控制是否启用 MySQL 会话持久化。
	Enabled bool `yaml:"enabled"`
	// DSN 是 go-sql-driver/mysql 连接串。
	DSN string `yaml:"dsn"`
	// MaxHistoryRecords 是每次请求加载的最近问答记录数量。
	MaxHistoryRecords int `yaml:"max-history-records"`
	// AutoMigrate 控制启动时是否自动创建 ai_session 表。
	AutoMigrate bool `yaml:"auto-migrate"`
	// MaxOpenConns 是 MySQL 最大打开连接数。
	MaxOpenConns int `yaml:"max-open-conns"`
	// MaxIdleConns 是 MySQL 最大空闲连接数。
	MaxIdleConns int `yaml:"max-idle-conns"`
	// ConnMaxLifetimeSeconds 是连接最大生命周期，单位秒。
	ConnMaxLifetimeSeconds int `yaml:"conn-max-lifetime-seconds"`
}

// ContextConfig 是模型上下文窗口管理配置。
type ContextConfig struct {
	// MaxInputTokens 是本次模型输入最多允许的估算 token 数。
	MaxInputTokens int `yaml:"max-input-tokens"`
	// ReservedOutputTokens 是给模型输出预留的 token 数。
	ReservedOutputTokens int `yaml:"reserved-output-tokens"`
	// MaxHistoryTokens 是历史消息最多允许占用的估算 token 数。
	MaxHistoryTokens int `yaml:"max-history-tokens"`
	// CharsPerToken 是字符到 token 的粗略换算比例。
	CharsPerToken int `yaml:"chars-per-token"`
}

// ToolsConfig 聚合所有工具的配置。
type ToolsConfig struct {
	// Tavily 是 web_search 工具的 Tavily HTTP API 配置。
	Tavily TavilyConfig `yaml:"tavily"`
}

// SkillsConfig 是本地 Skills Runtime 配置。
// directory 兼容 Java dodo-agent 的单目录写法；directories 用于 Go 版多目录扫描。
type SkillsConfig struct {
	// Directory 是单个技能根目录，每个子目录下包含一个 SKILL.md。
	Directory string `yaml:"directory"`
	// Directories 是多个技能根目录。
	Directories []string `yaml:"directories"`
	// AutoReload 控制每次读取前是否重新扫描目录，开发调试时建议开启。
	AutoReload bool `yaml:"auto-reload"`
}

// MCPConfig 聚合所有可选 MCP Server。
type MCPConfig struct {
	// Servers 是 application.yaml 中声明的 MCP server 列表。
	Servers []MCPServerConfig `yaml:"servers"`
}

// MCPServerConfig 描述一个 MCP server 的连接方式和注册策略。
type MCPServerConfig struct {
	// Name 是本地日志和工具名前缀使用的 server 名称。
	Name string `yaml:"name"`
	// Enabled 控制该 server 是否在启动时连接。
	Enabled bool `yaml:"enabled"`
	// Transport 支持 streamable-http、sse、command。
	Transport string `yaml:"transport"`
	// URL 是 streamable-http 或 sse transport 的 endpoint。
	URL string `yaml:"url"`
	// Command 是 command transport 的可执行文件。
	Command string `yaml:"command"`
	// Args 是 command transport 的命令参数。
	Args []string `yaml:"args"`
	// Headers 是 HTTP transport 的固定请求头，适合 Authorization 等鉴权信息。
	Headers map[string]string `yaml:"headers"`
	// ToolPrefix 为空时使用 name 作为前缀；设置为 "-" 可关闭前缀。
	ToolPrefix string `yaml:"tool-prefix"`
	// TimeoutSeconds 控制连接、列工具和调用工具的超时时间。
	TimeoutSeconds int `yaml:"timeout-seconds"`
	// DisableStandaloneSSE 只对 streamable-http 有效，禁用独立 SSE 长连接。
	DisableStandaloneSSE bool `yaml:"disable-standalone-sse"`
}

// ObservabilityConfig 聚合 Agent Runtime 的可观测性配置。
type ObservabilityConfig struct {
	// Trace 控制每次 Agent Run 的事件记录和回放能力。
	Trace TraceConfig `yaml:"trace"`
}

// TraceConfig 是本地 trace 文件记录配置。
type TraceConfig struct {
	// Enabled 控制是否保存 Agent Run trace。
	Enabled bool `yaml:"enabled"`
	// Directory 是 trace JSON 文件目录。
	Directory string `yaml:"directory"`
	// MaxEventContentChars 限制单个事件长文本字段的保存长度。
	MaxEventContentChars int `yaml:"max-event-content-chars"`
}

// TavilyConfig 是 Tavily 搜索工具配置。
type TavilyConfig struct {
	// Endpoint 是 Tavily Search API 地址。
	Endpoint string `yaml:"endpoint"`
	// APIKey 是 Tavily API 密钥；为空时 web_search 会降级到 mock 结果。
	APIKey string `yaml:"api-key"`
	// ProjectID 是可选项目 ID，会写入 X-Project-ID 请求头。
	ProjectID string `yaml:"project-id"`
	// SearchDepth 是 Tavily 搜索深度，例如 basic。
	SearchDepth string `yaml:"search-depth"`
	// MaxResults 是单次搜索最多返回的结果数。
	MaxResults int `yaml:"max-results"`
	// TimeoutSeconds 是 Tavily HTTP 请求超时时间，单位秒。
	TimeoutSeconds int `yaml:"timeout-seconds"`
}

// Default 返回应用配置默认值。
// 敏感字段不会给默认值，必须由 application.yaml 显式填写。
func Default() Config {
	return Config{
		Server: ServerConfig{
			Port: "8888",
		},
		Model: ModelConfig{
			BaseURL: "https://dashscope.aliyuncs.com/compatible-mode",
			Name:    "qwen3.6-flash-2026-04-16",
		},
		Agent: AgentConfig{
			MaxRounds: 5,
		},
		Memory: MemoryConfig{
			MaxHistoryRecords:      30,
			AutoMigrate:            true,
			MaxOpenConns:           10,
			MaxIdleConns:           5,
			ConnMaxLifetimeSeconds: 300,
		},
		Context: ContextConfig{
			MaxInputTokens:       12000,
			ReservedOutputTokens: 2000,
			MaxHistoryTokens:     4000,
			CharsPerToken:        4,
		},
		Tools: ToolsConfig{
			Tavily: TavilyConfig{
				Endpoint:       "https://api.tavily.com/search",
				SearchDepth:    "basic",
				MaxResults:     5,
				TimeoutSeconds: 20,
			},
		},
		Skills: SkillsConfig{
			AutoReload: true,
		},
		Observability: ObservabilityConfig{
			Trace: TraceConfig{
				Directory:            "traces",
				MaxEventContentChars: 4000,
			},
		},
	}
}

// Load 从 YAML 文件读取配置，并把缺省字段补齐。
func Load(path string) (Config, error) {
	cfg := Default()

	payload, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件 %q 失败: %w", path, err)
	}
	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件 %q 失败: %w", path, err)
	}

	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	if strings.TrimSpace(c.Server.Port) == "" {
		c.Server.Port = "8888"
	}
	c.Server.Port = strings.TrimPrefix(strings.TrimSpace(c.Server.Port), ":")

	if strings.TrimSpace(c.Model.BaseURL) == "" {
		c.Model.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode"
	}
	if strings.TrimSpace(c.Model.Name) == "" {
		c.Model.Name = "qwen3.6-flash-2026-04-16"
	}
	c.Model.BaseURL = strings.TrimSpace(c.Model.BaseURL)
	c.Model.APIKey = strings.TrimSpace(c.Model.APIKey)
	c.Model.Name = strings.TrimSpace(c.Model.Name)

	if c.Agent.MaxRounds <= 0 {
		c.Agent.MaxRounds = 5
	}

	c.Memory.DSN = strings.TrimSpace(c.Memory.DSN)
	if c.Memory.MaxHistoryRecords <= 0 {
		c.Memory.MaxHistoryRecords = 30
	}
	if c.Memory.MaxOpenConns <= 0 {
		c.Memory.MaxOpenConns = 10
	}
	if c.Memory.MaxIdleConns <= 0 {
		c.Memory.MaxIdleConns = 5
	}
	if c.Memory.ConnMaxLifetimeSeconds <= 0 {
		c.Memory.ConnMaxLifetimeSeconds = 300
	}

	if c.Context.MaxInputTokens <= 0 {
		c.Context.MaxInputTokens = 12000
	}
	if c.Context.ReservedOutputTokens < 0 {
		c.Context.ReservedOutputTokens = 0
	}
	if c.Context.MaxHistoryTokens <= 0 {
		c.Context.MaxHistoryTokens = 4000
	}
	if c.Context.CharsPerToken <= 0 {
		c.Context.CharsPerToken = 4
	}
	if c.Context.MaxHistoryTokens > c.Context.MaxInputTokens {
		c.Context.MaxHistoryTokens = c.Context.MaxInputTokens
	}

	if strings.TrimSpace(c.Tools.Tavily.Endpoint) == "" {
		c.Tools.Tavily.Endpoint = "https://api.tavily.com/search"
	}
	if strings.TrimSpace(c.Tools.Tavily.SearchDepth) == "" {
		c.Tools.Tavily.SearchDepth = "basic"
	}
	if c.Tools.Tavily.MaxResults <= 0 {
		c.Tools.Tavily.MaxResults = 5
	}
	if c.Tools.Tavily.TimeoutSeconds <= 0 {
		c.Tools.Tavily.TimeoutSeconds = 20
	}
	c.Tools.Tavily.Endpoint = strings.TrimSpace(c.Tools.Tavily.Endpoint)
	c.Tools.Tavily.APIKey = strings.TrimSpace(c.Tools.Tavily.APIKey)
	c.Tools.Tavily.ProjectID = strings.TrimSpace(c.Tools.Tavily.ProjectID)
	c.Tools.Tavily.SearchDepth = strings.TrimSpace(c.Tools.Tavily.SearchDepth)

	c.Skills.Directory = strings.TrimSpace(c.Skills.Directory)
	normalizedDirs := make([]string, 0, len(c.Skills.Directories)+1)
	if c.Skills.Directory != "" {
		normalizedDirs = append(normalizedDirs, c.Skills.Directory)
	}
	for _, dir := range c.Skills.Directories {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			normalizedDirs = append(normalizedDirs, dir)
		}
	}
	c.Skills.Directories = uniqueStrings(normalizedDirs)

	for i := range c.MCP.Servers {
		server := &c.MCP.Servers[i]
		server.Name = strings.TrimSpace(server.Name)
		server.Transport = strings.ToLower(strings.TrimSpace(server.Transport))
		server.URL = strings.TrimSpace(server.URL)
		server.Command = strings.TrimSpace(server.Command)
		server.ToolPrefix = strings.TrimSpace(server.ToolPrefix)
		if server.Transport == "" {
			server.Transport = "streamable-http"
		}
		if server.TimeoutSeconds <= 0 {
			server.TimeoutSeconds = 30
		}
		if server.Headers == nil {
			server.Headers = map[string]string{}
		}
		for k, v := range server.Headers {
			cleanKey := strings.TrimSpace(k)
			cleanValue := strings.TrimSpace(v)
			if cleanKey == "" || cleanValue == "" {
				delete(server.Headers, k)
				continue
			}
			if cleanKey != k {
				delete(server.Headers, k)
			}
			server.Headers[cleanKey] = cleanValue
		}
	}

	c.Observability.Trace.Directory = strings.TrimSpace(c.Observability.Trace.Directory)
	if c.Observability.Trace.Directory == "" {
		c.Observability.Trace.Directory = "traces"
	}
	if c.Observability.Trace.MaxEventContentChars <= 0 {
		c.Observability.Trace.MaxEventContentChars = 4000
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
