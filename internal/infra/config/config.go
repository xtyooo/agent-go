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
