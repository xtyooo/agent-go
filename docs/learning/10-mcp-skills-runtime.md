# Milestone 10 MCP + Skills Runtime

本阶段目标是把工具生态做成可插拔运行时，同时保留 Java dodo-agent 的 Skills 能力。

Java dodo-agent 的关键链路：

```text
HttpClientStreamableHttpTransport
-> McpClient.sync(...).initialize()
-> SyncMcpToolCallbackProvider.getToolCallbacks()
-> Agent defaultToolCallbacks(...)

SkillManager
-> FileSystemSkillRegistry
-> SkillPromptFormatter
-> ReadSkillTool
-> SkillsReactAgent
```

Go 版对应链路：

```text
application.yaml mcp.servers
-> internal/runtime/mcp.LoadTools
-> 官方 github.com/modelcontextprotocol/go-sdk/mcp ClientSession
-> MCP tools/list
-> ToolAdapter 转成本项目 tool.Tool
-> tool.Registry 统一注册
-> ReAct Agent 原有 tool_calls 主循环调用

application.yaml skills.directory
-> 扫描 skills/*/SKILL.md
-> 解析 description / allowedTools
-> Skills prompt 注入系统提示词
-> 模型按需调用 read_skill
-> read_skill 返回完整技能正文
```

## 已实现范围

- 新增 `internal/runtime/mcp`：
  - 支持官方 MCP Go SDK：`github.com/modelcontextprotocol/go-sdk/mcp`。
  - 支持 `streamable-http`、`sse`、`command` 三类 transport。
  - 支持 HTTP headers、超时、禁用 streamable HTTP 独立 SSE 连接。
  - 通过 `tools/list` 拉取远端工具清单。
  - 通过 `ToolAdapter` 把远端 MCP tool 转成本地 `tool.Tool`。
  - 通过 `CallTool` 把本地工具调用转发给远端 MCP server。
- 扩展 `tool.DefaultRegistryConfig`：
  - 新增 `ExtraTools []Tool`，用于接收 MCP 动态工具。
- 扩展 `application.yaml`：
  - 新增 `mcp.servers` 配置段，默认示例 server 为 disabled，不影响本地启动。
- 新增 `internal/runtime/skill`：
  - `Manager`：扫描技能目录、缓存元数据、读取技能内容。
  - `Metadata`：保存 name、description、skillFile、allowedTools。
  - `FormatPrompt`：生成 Java `SkillPromptFormatter` 风格的技能列表提示词。
- 新增 `read_skill` 工具：
  - 工具名：`read_skill`
  - 参数：`skill`
  - 返回：`success/content/error` JSON。
- 新增 `internal/agents/skills.Agent`：
  - 复用现有 ReAct tool_calls 主循环。
  - 使用 Skills 系统提示词。
  - `agent_type=skills` 写入 Memory。
- 新增 HTTP 入口：
  - `GET /agent/skills/stream`
  - 仍复用 `/agent/stop?conversationId=...`。

## MCP 工具命名

默认情况下，远端 MCP tool 会被注册成本地名字：

```text
mcp_<server>_<remote_tool>
```

例如 Tavily server 返回 `search`，本地工具名会是：

```text
mcp_tavily_search
```

这样可以避免和本地 `web_search`、`current_time`、`read_skill` 重名。

如果你确认不会重名，可以在配置里写：

```yaml
tool-prefix: "-"
```

这会直接使用远端工具名。

## 配置示例

```yaml
mcp:
  servers:
    - name: "tavily"
      enabled: false
      transport: "streamable-http"
      url: "https://mcp.tavily.com/mcp/"
      timeout-seconds: 30
      disable-standalone-sse: true
      tool-prefix: "mcp_tavily"
      headers:
        Authorization: "Bearer <your-mcp-token>"
```

本项目不会在工具层读取环境变量。鉴权信息需要显式写入 `application.yaml`，或由你自己的配置渲染流程在启动前生成该文件。

## 边界设计

Agent 层仍然只依赖：

```go
type Tool interface {
    Definition() Definition
    Execute(ctx context.Context, input Input) (Result, error)
}
```

MCP SDK 只出现在 `internal/runtime/mcp`。这样后续替换 SDK、增加鉴权、增加资源/Prompt 能力，都不会影响 ReAct、Plan-Execute、Skills Agent 主流程。

## 测试方式

基础回归：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
```

启动后测试 Skills：

```powershell
curl.exe -N "http://127.0.0.1:8888/agent/skills/stream?conversationId=skills-1&query=请使用code-review技能说明如何审查这段代码"
```

启用 MCP 后，启动日志中应看到：

```text
🧩 MCP Server 开始连接
✅ MCP Server 工具已加载
🧩 MCP Runtime 加载完成
```

当模型调用 MCP 工具时，应看到：

```text
🔌 MCP 工具调用开始
✅ MCP 工具调用完成
```

## 当前边界

- 已完成 MCP tools/list 和 tools/call 的基础闭环。
- 暂未实现 MCP resources/prompts/list 的 Agent 暴露。
- 暂未实现 MCP tool list changed 的热更新。
- 配置中的 `${...}` 不会被自动展开；如果需要环境变量渲染，建议后续在 Config Runtime 统一实现。
