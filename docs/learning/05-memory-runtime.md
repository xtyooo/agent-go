# Milestone 5 Memory Runtime

本阶段复刻 Java dodo-agent 中 `BaseAgent + ChatMemory + AiSessionService` 的最小闭环。

## 这个模块解决什么问题

Memory Runtime 解决两件事：

1. 每次请求开始前，根据 `conversationId` 加载最近 N 条历史问答，追加到模型上下文。
2. 每次请求结束后，把用户问题、最终回答、思考过程、工具列表、引用和耗时保存到 `ai_session`。

Java 侧对应链路：

```text
AgentController.webSearchStream
-> createPersistentChatMemory(conversationId, 30)
-> WebSearchReactAgent.stream(conversationId, query)
-> sessionService.saveQuestion(...)
-> Flux.doOnNext 收集 text/thinking
-> doFinally saveSessionResult(...)
```

Go 侧对应链路：

```text
AgentHandler.ChatStream
-> ReactAgent.Run
-> appendHistory
-> saveRunQuestion
-> scheduleRounds
-> send 捕获 text/thinking/tool/reference
-> persistRun
```

## 核心接口

核心接口在 `internal/runtime/memory`：

```go
type Store interface {
    FindRecent(ctx context.Context, sessionID string, maxRecords int) ([]SessionRecord, error)
    SaveQuestion(ctx context.Context, req SaveQuestionRequest) (SessionRecord, error)
    UpdateAnswer(ctx context.Context, req UpdateAnswerRequest) error
    Close() error
}
```

`ReactAgent` 只依赖这个接口，不直接依赖 MySQL。

## 配置方式

在 `application.yaml` 中开启：

```yaml
memory:
  enabled: true
  dsn: "root:password@tcp(127.0.0.1:3306)/agent_go?charset=utf8mb4&parseTime=true&loc=Local"
  max-history-records: 30
  auto-migrate: true
  max-open-conns: 10
  max-idle-conns: 5
  conn-max-lifetime-seconds: 300
```

启动前需要先创建数据库：

```sql
CREATE DATABASE IF NOT EXISTS agent_go
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_unicode_ci;
```

`auto-migrate: true` 时，服务启动会自动创建 `ai_session` 表。

## 和其他模块的边界

- HTTP 层只负责传入 `conversationId`，不关心数据库。
- Agent 层负责决定什么时候加载历史、什么时候保存结果。
- Memory 层只负责持久化和查询，不理解 ReAct 轮次。
- Model 层不知道历史来自哪里，只接收最终拼好的 `messages`。

## 当前实现和 Java 的差异

- Go 版暂未实现 `SessionController` 的会话列表、详情、删除接口。
- Go 版暂未生成推荐问题，因此 `recommend` 通常为空。
- Go 版没有把 tool role message 保存为历史，只保存用户问题和最终回答。这和 Java `createPersistentChatMemory` 一致。
- Go 版和 Java 一样，先加载历史，再保存当前问题，避免当前问题被重复塞进本轮 prompt。
- Go 版保存回答发生在 Agent goroutine 退出时，对齐 Java `doFinally` 的收口语义。

## 验证方式

1. 配置 MySQL 并开启 `memory.enabled`。
2. 启动服务。
3. 用同一个 `conversationId` 连续请求两次。
4. 第二次日志应出现：

```text
📝 会话历史已加载
```

5. 查询数据库：

```sql
SELECT id, session_id, question, LEFT(answer, 100), tools, first_response_time, total_response_time
FROM ai_session
ORDER BY id DESC;
```

可以看到每次问答记录已经写入。
