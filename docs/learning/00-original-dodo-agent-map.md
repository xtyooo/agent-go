1. 这个项目有哪些入口 API？
/agent/chat/stream  WebSearchReactAgent
/agent/file/stream  FileReactAgent
/agent/pptx/stream  PPTBuilderAgent
/agent/deep/stream  PlanExecuteAgent
/agent/stop         AgentTaskManager.stopTask
/file/upload          FileManageService.uploadFile
/session/list         AiSessionService



2. 三类 Agent Runtime 分别是什么？
ReAct Runtime:
WebSearchReactAgent / FileReactAgent / SkillsReactAgent
流程是 messages -> LLM stream -> tool_call -> execute tools -> tool response -> next round -> final answer

Plan-Execute Runtime:
PlanExecuteAgent
流程是 clarify -> research topic -> plan -> execute tasks -> critique -> repeat -> summarize

State Machine Runtime:
PPTBuilderAgent
流程是 intent -> INIT/REQUIREMENT/TEMPLATE/OUTLINE/SEARCH/SCHEMA/RENDER/SUCCESS/FAILED



3. BaseAgent / AgentTaskManager / ContextCompactor 分别解决什么问题？
BaseAgent:
统一响应、历史加载、计时、工具记录、推荐问题、保存结果

AgentTaskManager:
conversationId 互斥、Redis SET NX TTL、Pub/Sub 停止、Disposable 取消

ContextCompactor:
旧工具结果压缩、超长上下文摘要、保护关键工具结果

FileContentService:
把“文件内容加载/RAG 检索”做成 Tool，让 FileAgent 不直接依赖检索细节

AgentResponse / AgentStreamEvent:
统一 SSE 协议


4. Go 复刻时哪些模块先做，哪些模块后做？


先做：
- SSE 事件协议：AgentEvent、thinking/text/error/tool_start/tool_end/reference/complete。
- Model Runtime：封装 OpenAI-compatible 调用和流式输出。
- Tool Runtime：Tool 接口、ToolRegistry、参数 schema、执行结果。
- WebSearchReactAgent：手写 ReAct Loop，验证 Agent Runtime 核心闭环。

中间做：
- Memory Runtime：MySQL 保存和加载会话历史。
- Task Runtime：conversationId 互斥、context cancel、Redis 分布式停止。
- FileContentService / RAG：文件上传、解析、chunk、embedding、pgvector 检索。
- Context Runtime：token 估算、工具结果压缩、摘要压缩。

后做：
- PlanExecuteAgent：clarify、plan、execute、critique、summarize。
- MCP / Skills Runtime：MCP 工具接入、skills 目录扫描、read_skill。
- PPTBuilderAgent：意图识别、状态机、断点恢复、Python/Node 渲染 sidecar。

暂时不做：
- 一开始不追求完整前端。
- 一开始不复刻所有文件格式解析。
- 一开始不做纯 Go PPTX 渲染。
- 一开始不直接依赖 langchaingo 的 Agent 封装。



5. 我对原项目 Runtime 的理解

这个项目的核心不是 Controller，也不是 Spring AI，而是 Agent Runtime。
Controller 只负责接收请求和返回 SSE；Agent 负责状态循环；Tool 负责外部能力；
Memory 负责历史上下文；TaskManager 负责并发和取消；ContextCompactor 负责控制上下文大小。
Go 复刻时应该先实现这些边界，而不是逐个翻译 Java 类。
