# Milestone 11 PPTBuilder 状态机

本阶段严格参考 Java dodo-agent 的 PPTBuilder 主链路：

```text
PPTBuilderAgent
-> PptIntentRecognizer
-> PptStateStrategyFactory
-> Requirement/Search/Template/Outline/Schema/Render/Success Strategy
-> AiPptInst 持久化状态
```

Go 版当前链路：

```text
GET /agent/pptx/stream
-> internal/agents/pptx.Agent
-> resolveIntent(CREATE_PPT/MODIFY_PPT/RESUME_PPT)
-> MemoryStore(AiPptInst 等价结构)
-> 状态机推进
-> SSE thinking/tool/text/complete
```

## 已实现范围

- 新增 `internal/agents/pptx`：
  - `Status`：`INIT / REQUIREMENT / SEARCH / TEMPLATE / OUTLINE / SCHEMA / RENDER / SUCCESS / FAILED`
  - `Intent`：`CREATE_PPT / MODIFY_PPT / RESUME_PPT`
  - `Instance`：对齐 Java `AiPptInst`
  - `Store`：PPT 实例存储抽象
  - `MemoryStore`：进程内断点状态保存
  - `Agent`：PPTBuilder 状态机编排
- 新增 HTTP 入口：
  - `GET /agent/pptx/stream`
  - 继续复用 `/agent/stop?conversationId=...`
- 状态流程：
  - 需求澄清：模型判断是否可以开始生成。
  - 信息收集：调用 `web_search`，失败时降级继续。
  - 模板选择：从内置模板中选择。
  - 大纲生成：模型生成页面大纲。
  - Schema 生成：模型生成 PPT Schema JSON。
  - 渲染：当前用 mock URL，后续替换 Python/MinIO。
  - 总结：流式输出最终说明。
- 支持同会话继续：
  - 用户输入“继续/重试/resume/retry”时尝试从最新未完成实例继续。
- 支持修改意图：
  - 已成功生成后，用户输入“修改/调整/优化/更新”等关键词时，基于旧 Schema 生成新 Schema 并重新 mock 渲染。

## 和 Java 的映射

Java：

```text
AiPptInstService.createInst / getLatestInst / update...
PptStateStrategyFactory.executeNextState
PptPythonRenderService.renderPpt
```

Go：

```text
pptx.Store.Create / Latest / Update
pptx.Agent.continueStateMachine
pptx.Agent.render 当前返回 mock://ppt/<conversation>/<id>.pptx
```

## 测试方式

```powershell
curl.exe -N "http://127.0.0.1:8888/agent/pptx/stream?conversationId=ppt-1&query=生成一份AI Agent技术分享PPT，6页，科技风，面向研发团队"
```

正常日志应看到：

```text
📊 PPTBuilder Agent 已启动
🧭 PPTBuilder 意图识别完成
✅ PPTBuilder 需求分析完成
✅ PPTBuilder 信息收集完成
✅ PPTBuilder 模板选择完成
✅ PPTBuilder 大纲生成完成
✅ PPTBuilder Schema 生成完成
✅ PPTBuilder 渲染完成
✅ PPTBuilder 总结输出完成
🏁 PPTBuilder Agent 已完成
```

## 当前边界

- PPT 实例目前保存在进程内 `MemoryStore`，服务重启后不会恢复。
- 渲染阶段当前是 mock URL，没有调用 Python 渲染器、MinIO 或真实 PPTX 生成。
- 模板目前是内置 Go 结构，未接数据库模板表。
- 图片生成暂未接入，Schema 中 image/background 字段只保留提示词和空 URL。

下一步建议：

- 用 GORM 实现 `ai_ppt_inst` 和 `ai_ppt_template`。
- 增加 `Renderer` 接口，把 mock 渲染替换成 Python/Node sidecar。
- 增加真实 PPT 文件下载接口或静态文件托管。
