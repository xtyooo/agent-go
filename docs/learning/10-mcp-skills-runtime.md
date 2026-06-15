# Milestone 10 MCP + Skills Runtime

本阶段先落地本地 Skills Runtime 小闭环，严格参考 Java dodo-agent 的手动 Skills 实现：

```text
SkillManager
FileSystemSkillRegistry
SkillPromptFormatter
ReadSkillTool
SkillsReactAgent
```

Go 版当前链路：

```text
application.yaml skills.directory
-> 扫描 skills/*/SKILL.md
-> 解析 description / allowedTools
-> Skills prompt 注入系统提示词
-> 模型按需调用 read_skill
-> read_skill 返回完整技能正文
-> ReAct 下一轮基于技能指令回答
```

## 已实现范围

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
- 新增示例技能：
  - `skills/code-review/SKILL.md`
- 新增单元测试：
  - 技能目录扫描
  - front matter 解析
  - prompt 格式
  - `read_skill` 工具返回

## 和 Java 的映射

Java：

```text
SkillManager
SkillRegistry
FileSystemSkillRegistry
ReadSkillTool
SkillsReactAgent
```

Go：

```text
internal/runtime/skill.Manager
internal/runtime/skill.Metadata
internal/runtime/tool.ReadSkillTool
internal/agents/skills.Agent
```

## 配置

```yaml
skills:
  directory: "skills"
  auto-reload: true
```

`directory` 下面每个子目录都是一个技能目录，目录内必须有 `SKILL.md`：

```text
skills/
  code-review/
    SKILL.md
```

## 测试方式

启动服务后：

```powershell
curl.exe -N "http://127.0.0.1:8888/agent/skills/stream?conversationId=skills-1&query=请使用code-review技能说明如何审查这段代码"
```

正常情况下，日志中应看到：

```text
🧩 Skills Runtime 扫描完成
🧩 技能已加载 skill=code-review
```

前端/SSE 会看到 `tool_start/read_skill`、`tool_end/read_skill`，随后模型基于技能内容继续回答。

## 当前边界

本次只实现本地 Skills Runtime，没有接入官方 MCP Go SDK。

下一步 MCP 接入建议：

- 定义 `internal/runtime/mcp` 包。
- 通过配置加载 MCP server。
- 把 MCP tools 转换为现有 `tool.Tool` 接口。
- 保持 Agent 只依赖 `tool.Registry`，不要让 Agent 直接依赖 MCP SDK。
