<template>
  <div class="app-shell" :class="{ 'sidebar-open': sidebarOpen, 'details-open': detailsOpen }" @keydown.esc="closeOverlays">
    <a class="skip-link" href="#chat-main">跳到聊天内容</a>
    <button
      v-if="sidebarOpen || detailsOpen"
      class="scrim"
      type="button"
      aria-label="关闭浮层"
      @click="closeOverlays"
    ></button>
    <aside class="sidebar" aria-label="会话">
      <div class="sidebar-header">
        <div class="brand">
          <strong>KimoAgent</strong>
        </div>
        <button class="icon-button mobile-only" type="button" title="关闭侧栏" @click="sidebarOpen = false">
          <PanelLeftClose :size="18" />
        </button>
      </div>

      <nav class="sidebar-nav" aria-label="主导航">
        <button class="nav-item" type="button" @click="startNewConversation">
          <Plus :size="18" />
          <span>新聊天</span>
        </button>
        <label class="nav-item nav-search">
          <Search :size="18" />
          <input v-model.trim="sessionKeyword" type="search" placeholder="搜索聊天" @keydown.enter="loadSessions" />
        </label>
        <button class="nav-item muted" type="button">
          <Library :size="18" />
          <span>库</span>
        </button>
        <button class="nav-item muted" type="button">
          <Folder :size="18" />
          <span>项目</span>
        </button>
        <button class="nav-item muted" type="button">
          <Blocks :size="18" />
          <span>应用</span>
        </button>
        <button class="nav-item muted" type="button">
          <Bot :size="18" />
          <span>Codex</span>
        </button>
      </nav>

      <div class="recent-heading">最近</div>

      <div class="session-list" role="list">
        <button
          v-for="session in sessions"
          :key="session.sessionId"
          class="session-item"
          :class="{ active: session.sessionId === activeConversationId }"
          type="button"
          role="listitem"
          @click="openSession(session.sessionId)"
        >
          <MessageSquare :size="16" />
          <span>
            <strong>{{ session.title || session.sessionId }}</strong>
            <small>{{ sessionPreview(session) }}</small>
          </span>
          <time>{{ formatDateTime(session.updateTime) }}</time>
        </button>

        <div v-if="sessionsLoading" class="empty-state compact">正在读取历史会话...</div>
        <div v-else-if="sessionError" class="empty-state compact warning">{{ sessionError }}</div>
        <div v-else-if="!sessions.length" class="empty-state compact">还没有保存的会话</div>
      </div>

      <div class="sidebar-footer">
        <span class="user-dot">YA</span>
        <span>
          <strong>杨胜鑫</strong>
          <small>Plus</small>
        </span>
      </div>
    </aside>

    <main id="chat-main" class="chat-layout" tabindex="-1">
      <header class="chat-header">
        <div class="header-left">
          <button class="icon-button" type="button" title="会话列表" @click="sidebarOpen = !sidebarOpen">
            <PanelLeft :size="20" />
          </button>
          <div>
            <h1>{{ activeTitle }}</h1>
            <p>{{ headerSubtitle }}</p>
          </div>
        </div>
        <div class="header-actions">
          <button class="share-button" type="button" title="分享">
            <Share2 :size="17" />
            <span>分享</span>
          </button>
          <button class="icon-button" type="button" title="复制最后回答" :disabled="!lastAssistantText" @click="copyLastAnswer">
            <Copy :size="18" />
          </button>
          <button class="icon-button" type="button" title="重命名会话" :disabled="!canManageSession" @click="promptRename">
            <Pencil :size="18" />
          </button>
          <button class="icon-button danger" type="button" title="删除会话" :disabled="!canManageSession" @click="removeActiveSession">
            <Trash2 :size="18" />
          </button>
          <button class="icon-button" type="button" title="运行细节" @click="detailsOpen = !detailsOpen">
            <PanelRight :size="20" />
          </button>
          <button class="icon-button" type="button" title="更多">
            <MoreHorizontal :size="20" />
          </button>
        </div>
      </header>

      <section ref="scrollPanel" class="message-scroll" aria-live="polite">
        <div v-if="!messages.length" class="welcome">
          <div class="welcome-mark">
            <Sparkles :size="34" />
          </div>
          <h2>有什么可以帮忙的？</h2>
          <div class="prompt-grid">
            <button v-for="prompt in starterPrompts" :key="prompt" type="button" @click="useStarterPrompt(prompt)">
              {{ prompt }}
            </button>
          </div>
        </div>

        <article v-for="message in messages" :key="message.id" class="message-row" :class="message.role">
          <div class="avatar" aria-hidden="true">
            <UserRound v-if="message.role === 'user'" :size="18" />
            <Bot v-else :size="18" />
          </div>
          <div class="message-body">
            <div class="message-meta">
              <strong>{{ message.role === "user" ? "你" : "KimoAgent" }}</strong>
              <span>{{ formatDateTime(message.createdAt) }}</span>
            </div>

            <div v-if="message.role === 'assistant' && message.process?.length" class="process-list">
              <details v-for="item in message.process" :key="item.id" :open="item.status === 'running'">
                <summary>
                  <span :class="['process-dot', item.status]"></span>
                  {{ item.title }}
                  <small>{{ item.meta }}</small>
                </summary>
                <pre v-if="item.detail">{{ item.detail }}</pre>
              </details>
            </div>

            <div v-if="message.content" class="markdown-body" v-html="renderMarkdown(message.content)"></div>
            <div v-else-if="message.role === 'assistant' && isRunning" class="typing">
              <span></span>
              <span></span>
              <span></span>
            </div>

            <div v-if="message.references?.length" class="reference-list">
              <a
                v-for="reference in message.references"
                :key="reference.url || reference.title"
                :href="reference.url"
                target="_blank"
                rel="noreferrer"
              >
                <ExternalLink :size="14" />
                {{ reference.title || reference.url }}
              </a>
            </div>
          </div>
        </article>
      </section>

      <footer class="composer-shell">
        <div v-if="errorText" class="error-banner">
          <CircleAlert :size="16" />
          {{ errorText }}
        </div>
        <div v-if="agentPickerOpen" class="agent-menu" role="menu" aria-label="选择 Agent">
          <button
            v-for="agent in AGENT_OPTIONS"
            :key="agent.value"
            class="agent-menu-item"
            :class="{ active: agentType === agent.value }"
            type="button"
            role="menuitemradio"
            :aria-checked="agentType === agent.value"
            @click="chooseAgent(agent.value)"
          >
            <span>
              <strong>{{ agent.label }}</strong>
              <small>{{ agent.description }}</small>
            </span>
            <Check v-if="agentType === agent.value" :size="16" />
          </button>
          <button class="agent-menu-advanced" type="button" @click="openDetails">
            <PanelRight :size="16" />
            高级运行设置
          </button>
        </div>
        <div class="composer">
          <button class="composer-icon" type="button" title="新建会话" @click="startNewConversation">
            <Plus :size="20" />
          </button>
          <textarea
            ref="composerInput"
            v-model="draft"
            rows="1"
            maxlength="4000"
            aria-label="给 KimoAgent 发送消息"
            placeholder="给 KimoAgent 发送消息"
            @input="resizeComposer"
            @keydown="handleComposerKeydown"
          ></textarea>
          <div class="composer-controls">
            <button
              class="agent-pill"
              type="button"
              title="选择 Agent"
              :aria-expanded="agentPickerOpen"
              aria-haspopup="menu"
              @click="agentPickerOpen = !agentPickerOpen"
            >
              {{ selectedAgent.label }}
              <ChevronDown :size="14" />
            </button>
            <button class="composer-icon" type="button" title="语音">
              <Mic :size="19" />
            </button>
            <button v-if="isRunning" class="send-button stop" type="button" title="停止生成" @click="stopRun">
              <Square :size="18" />
            </button>
            <button v-else class="send-button" type="button" title="发送" :disabled="!canSend" @click="sendMessage">
              <SendHorizontal :size="19" />
            </button>
          </div>
        </div>
        <div class="composer-meta">
          <span>KimoAgent 也可能会犯错，请核查重要信息。</span>
          <span>{{ draft.length }}/4000</span>
        </div>
      </footer>
    </main>

    <aside class="details-panel" aria-label="运行细节">
      <div class="details-header">
        <div>
          <strong>运行设置</strong>
          <small>{{ statusLabel }}</small>
        </div>
        <button class="icon-button" type="button" title="关闭细节" @click="detailsOpen = false">
          <PanelRightClose :size="18" />
        </button>
      </div>

      <section class="settings-section">
        <label>Agent 类型</label>
        <div class="agent-select">
          <button
            v-for="agent in AGENT_OPTIONS"
            :key="agent.value"
            :class="{ active: agentType === agent.value }"
            type="button"
            @click="agentType = agent.value"
          >
            <strong>{{ agent.label }}</strong>
            <small>{{ agent.description }}</small>
          </button>
        </div>
      </section>

      <section class="settings-section">
        <div class="setting-row">
          <label for="temperature">Temperature</label>
          <output>{{ temperature.toFixed(1) }}</output>
        </div>
        <input id="temperature" v-model.number="temperature" type="range" min="0" max="1" step="0.1" />
      </section>

      <section class="settings-section">
        <div class="setting-row">
          <label for="maxTurns">Max Turns</label>
          <output>{{ maxTurns }}</output>
        </div>
        <input id="maxTurns" v-model.number="maxTurns" type="range" min="1" max="20" step="1" />
      </section>

      <section class="settings-section">
        <div class="metrics">
          <div>
            <span>会话 ID</span>
            <strong>{{ activeConversationId || "-" }}</strong>
          </div>
          <div>
            <span>消息数</span>
            <strong>{{ messages.length }}</strong>
          </div>
          <div>
            <span>事件数</span>
            <strong>{{ streamEvents.length }}</strong>
          </div>
          <div>
            <span>首响</span>
            <strong>{{ firstResponseLabel }}</strong>
          </div>
        </div>
      </section>

      <section class="settings-section grow">
        <div class="section-title">
          <label>实时事件</label>
          <button class="text-button" type="button" @click="streamEvents = []">清空</button>
        </div>
        <div class="event-log">
          <div v-for="event in streamEvents" :key="event.id" class="event-item">
            <span :class="['event-type', event.type]">{{ event.type }}</span>
            <p>{{ eventSummary(event) }}</p>
          </div>
          <div v-if="!streamEvents.length" class="empty-state compact">等待下一次运行事件</div>
        </div>
      </section>
    </aside>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  Blocks,
  Bot,
  Check,
  ChevronDown,
  CircleAlert,
  Copy,
  ExternalLink,
  Folder,
  Library,
  MessageSquare,
  Mic,
  MoreHorizontal,
  PanelLeft,
  PanelLeftClose,
  PanelRight,
  PanelRightClose,
  Pencil,
  Plus,
  Search,
  SendHorizontal,
  Share2,
  Sparkles,
  Square,
  Trash2,
  UserRound
} from "@lucide/vue";
import {
  AGENT_OPTIONS,
  buildStreamURL,
  deleteSession,
  fetchSessionDetail,
  fetchSessions,
  findAgent,
  renameSession,
  stopAgent
} from "./api";
import { compactText, createConversationId, formatDateTime, formatDuration, parseMaybeJSON } from "./format";
import { renderMarkdown } from "./markdown";

const starterPrompts = [
  "帮我总结一下这个 Agent 项目的架构",
  "用 Deep Agent 制定一个重构计划",
  "搜索最近的 Go SSE 最佳实践",
  "分析当前技能系统还能怎么扩展"
];

const sidebarOpen = ref(false);
const detailsOpen = ref(false);
const agentPickerOpen = ref(false);
const sessions = ref([]);
const sessionsLoading = ref(false);
const sessionError = ref("");
const sessionKeyword = ref("");
const activeConversationId = ref("");
const activeSessionTitle = ref("");
const messages = ref([]);
const draft = ref("");
const agentType = ref("websearch");
const temperature = ref(0.2);
const maxTurns = ref(5);
const isRunning = ref(false);
const errorText = ref("");
const streamEvents = ref([]);
const eventSource = ref(null);
const currentAssistantId = ref("");
const runStartedAt = ref(0);
const firstResponseMs = ref(0);
const scrollPanel = ref(null);
const composerInput = ref(null);
const pendingToolCallIds = new Map();

const selectedAgent = computed(() => findAgent(agentType.value));
const activeTitle = computed(() => activeSessionTitle.value || compactText(firstUserMessage.value, "新会话"));
const firstUserMessage = computed(() => messages.value.find((item) => item.role === "user")?.content || "");
const headerSubtitle = computed(() => {
  if (isRunning.value) return "正在生成回复";
  if (activeConversationId.value) return `${selectedAgent.value.label} · 已连接后端记忆`;
  return "选择历史会话，或开启一个新问题";
});
const statusLabel = computed(() => (isRunning.value ? "运行中" : "空闲"));
const canSend = computed(() => draft.value.trim().length > 0 && !isRunning.value);
const canManageSession = computed(() => Boolean(activeConversationId.value) && messages.value.length > 0 && !isRunning.value);
const lastAssistantText = computed(() => {
  const assistants = messages.value.filter((item) => item.role === "assistant" && item.content);
  return assistants.at(-1)?.content || "";
});
const firstResponseLabel = computed(() => formatDuration(firstResponseMs.value));

onMounted(async () => {
  await loadSessions();
  focusComposer();
});

onBeforeUnmount(() => {
  closeStream();
});

watch(messages, () => scrollToBottom(), { deep: true });

async function loadSessions(options = {}) {
  const silent = Boolean(options.silent);
  if (!silent) sessionsLoading.value = true;
  try {
    if (!silent) sessionError.value = "";
    const payload = await fetchSessions({
      keyword: sessionKeyword.value,
      agentType: "",
      limit: 50
    });
    sessions.value = payload.items;
  } catch (error) {
    if (!silent) {
      sessionError.value = "历史会话暂不可用";
    }
  } finally {
    if (!silent) sessionsLoading.value = false;
  }
}

async function openSession(sessionId) {
  if (isRunning.value) return;
  errorText.value = "";
  try {
    const records = await fetchSessionDetail(sessionId);
    activeConversationId.value = sessionId;
    activeSessionTitle.value = records[0]?.sessionName || compactText(records[0]?.question, sessionId);
    agentType.value = records.at(-1)?.agentType || agentType.value;
    messages.value = records.flatMap(recordToMessages);
    streamEvents.value = [];
    firstResponseMs.value = records.at(-1)?.firstResponseTime || 0;
    sidebarOpen.value = false;
  } catch (error) {
    errorText.value = error.message || "读取会话详情失败";
  }
}

function recordToMessages(record) {
  const pair = [];
  if (record.question) {
    pair.push({
      id: `user-${record.id}`,
      role: "user",
      content: record.question,
      createdAt: record.createTime
    });
  }
  if (record.answer || record.thinking || record.reference || record.tools) {
    pair.push({
      id: `assistant-${record.id}`,
      role: "assistant",
      content: record.answer || "",
      createdAt: record.updateTime || record.createTime,
      process: historicalProcess(record),
      references: parseReferences(record.reference)
    });
  }
  return pair;
}

function historicalProcess(record) {
  const process = [];
  if (record.thinking) {
    process.push({
      id: `thinking-${record.id}`,
      title: "思考过程",
      status: "success",
      meta: "历史记录",
      detail: record.thinking
    });
  }
  if (record.tools) {
    process.push({
      id: `tools-${record.id}`,
      title: "使用工具",
      status: "success",
      meta: record.tools,
      detail: record.tools
    });
  }
  return process;
}

function parseReferences(value) {
  const parsed = parseMaybeJSON(value);
  if (Array.isArray(parsed)) {
    return parsed.map((item) => ({
      title: item.title || item.name || item.url,
      url: item.url || item.link || ""
    })).filter((item) => item.url);
  }
  if (parsed?.results && Array.isArray(parsed.results)) {
    return parsed.results.map((item) => ({
      title: item.title || item.url,
      url: item.url || ""
    })).filter((item) => item.url);
  }
  return [];
}

function startNewConversation() {
  if (isRunning.value) return;
  closeStream();
  activeConversationId.value = "";
  activeSessionTitle.value = "";
  messages.value = [];
  streamEvents.value = [];
  errorText.value = "";
  firstResponseMs.value = 0;
  pendingToolCallIds.clear();
  sidebarOpen.value = false;
  focusComposer();
}

function chooseAgent(value) {
  agentType.value = value;
  agentPickerOpen.value = false;
  focusComposer();
}

function openDetails() {
  agentPickerOpen.value = false;
  detailsOpen.value = true;
}

function closeOverlays() {
  sidebarOpen.value = false;
  detailsOpen.value = false;
  agentPickerOpen.value = false;
}

function useStarterPrompt(prompt) {
  draft.value = prompt;
  focusComposer();
}

async function sendMessage() {
  const query = draft.value.trim();
  if (!query || isRunning.value) return;

  closeStream();
  errorText.value = "";
  const conversationId = activeConversationId.value || createConversationId();
  activeConversationId.value = conversationId;
  if (!activeSessionTitle.value) activeSessionTitle.value = compactText(query);

  const userMessage = {
    id: `user-${Date.now()}`,
    role: "user",
    content: query,
    createdAt: new Date().toISOString()
  };
  const assistantMessage = {
    id: `assistant-${Date.now()}`,
    role: "assistant",
    content: "",
    createdAt: new Date().toISOString(),
    process: [],
    references: []
  };
  messages.value.push(userMessage, assistantMessage);
  currentAssistantId.value = assistantMessage.id;
  draft.value = "";
  resizeComposer();

  isRunning.value = true;
  runStartedAt.value = performance.now();
  firstResponseMs.value = 0;
  streamEvents.value = [];
  pendingToolCallIds.clear();

  const url = buildStreamURL({
    query,
    conversationId,
    agentType: agentType.value,
    temperature: temperature.value,
    maxTurns: maxTurns.value
  });

  const source = new EventSource(url);
  eventSource.value = source;
  source.onmessage = (message) => {
    try {
      handleStreamEvent(JSON.parse(message.data));
    } catch (error) {
      failRun(`事件解析失败：${error.message}`);
    }
  };
  source.onerror = () => {
    if (eventSource.value) {
      failRun("SSE 连接异常，请确认 Go 服务和模型配置可用");
    }
  };
}

function handleStreamEvent(event) {
  const typedEvent = {
    id: `${Date.now()}-${streamEvents.value.length}`,
    ...event
  };
  streamEvents.value.push(typedEvent);

  if (!firstResponseMs.value && ["text", "thinking", "tool_start", "tool_end"].includes(event.type)) {
    firstResponseMs.value = Math.max(1, Math.round(performance.now() - runStartedAt.value));
  }

  const assistant = currentAssistant();
  if (!assistant) return;

  if (event.type === "text") {
    assistant.content += event.content || "";
    return;
  }

  if (event.type === "thinking") {
    assistant.process.push({
      id: typedEvent.id,
      title: "思考过程",
      status: "running",
      meta: formatDateTime(event.time),
      detail: event.content || "正在分析"
    });
    return;
  }

  if (event.type === "tool_start") {
    const processId = event.toolCallId || typedEvent.id;
    if (event.toolName) {
      pendingToolCallIds.set(event.toolName, processId);
    }
    assistant.process.push({
      id: processId,
      title: `调用工具：${event.toolName || "unknown"}`,
      status: "running",
      meta: "执行中",
      detail: event.arguments || ""
    });
    return;
  }

  if (event.type === "tool_end") {
    const id = event.toolCallId || pendingToolCallIds.get(event.toolName) || typedEvent.id;
    const existing = assistant.process.find((item) => item.id === id);
    if (existing) {
      existing.status = "success";
      existing.meta = "已完成";
      existing.detail = event.result || event.content || existing.detail;
    } else {
      assistant.process.push({
        id,
        title: `工具完成：${event.toolName || "unknown"}`,
        status: "success",
        meta: "已完成",
        detail: event.result || event.content || ""
      });
    }
    if (event.toolName) {
      pendingToolCallIds.delete(event.toolName);
    }
    return;
  }

  if (event.type === "reference") {
    assistant.references = parseReferences(event.content);
    assistant.process.push({
      id: typedEvent.id,
      title: "引用资料",
      status: "success",
      meta: event.count ? `${event.count} 条` : "",
      detail: event.content || ""
    });
    return;
  }

  if (event.type === "recommend") {
    assistant.process.push({
      id: typedEvent.id,
      title: "推荐问题",
      status: "success",
      meta: "",
      detail: event.content || JSON.stringify(event.data || {}, null, 2)
    });
    return;
  }

  if (event.type === "error") {
      failRun(event.message || event.content || "Agent 返回错误");
    return;
  }

  if (event.type === "complete") {
      finishRun();
  }
}

async function stopRun() {
  if (!isRunning.value || !activeConversationId.value) return;
  try {
    await stopAgent(activeConversationId.value);
  } catch (error) {
    errorText.value = error.message || "停止失败";
  } finally {
    finishRun();
  }
}

function finishRun(processStatus = "success") {
  settleRunningProcesses(processStatus);
  closeStream();
  isRunning.value = false;
  currentAssistantId.value = "";
  pendingToolCallIds.clear();
  loadSessions({ silent: true });
}

function failRun(message) {
  errorText.value = message;
  const assistant = currentAssistant();
  if (assistant) {
    assistant.process.push({
      id: `error-${Date.now()}`,
      title: "运行异常",
      status: "error",
      meta: "",
      detail: message
    });
  }
  finishRun("error");
}

function settleRunningProcesses(status) {
  const assistant = currentAssistant();
  if (!assistant?.process) return;
  assistant.process.forEach((item) => {
    if (item.status === "running") {
      item.status = status;
      item.meta = status === "error" ? "已中断" : "已完成";
    }
  });
}

function closeStream() {
  if (eventSource.value) {
    eventSource.value.close();
    eventSource.value = null;
  }
}

function currentAssistant() {
  return messages.value.find((item) => item.id === currentAssistantId.value);
}

async function promptRename() {
  const nextName = window.prompt("会话名称", activeTitle.value);
  if (nextName === null) return;
  const name = nextName.trim();
  try {
    await renameSession(activeConversationId.value, name);
    activeSessionTitle.value = name || compactText(firstUserMessage.value, activeConversationId.value);
    await loadSessions();
  } catch (error) {
    errorText.value = error.message || "重命名失败";
  }
}

async function removeActiveSession() {
  const confirmed = window.confirm("确定删除当前会话？这个操作会删除后端数据库里的历史记录。");
  if (!confirmed) return;
  try {
    await deleteSession(activeConversationId.value);
    startNewConversation();
    await loadSessions();
  } catch (error) {
    errorText.value = error.message || "删除失败";
  }
}

async function copyLastAnswer() {
  if (!lastAssistantText.value) return;
  try {
    await navigator.clipboard.writeText(lastAssistantText.value);
  } catch (error) {
    errorText.value = "复制失败，请手动选择文本复制";
  }
}

function handleComposerKeydown(event) {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    sendMessage();
  }
}

function resizeComposer() {
  nextTick(() => {
    const node = composerInput.value;
    if (!node) return;
    node.style.height = "auto";
    node.style.height = `${Math.min(node.scrollHeight, 180)}px`;
  });
}

function scrollToBottom() {
  nextTick(() => {
    const node = scrollPanel.value;
    if (!node) return;
    node.scrollTop = node.scrollHeight;
  });
}

function focusComposer() {
  nextTick(() => {
    composerInput.value?.focus();
    resizeComposer();
  });
}

function sessionPreview(session) {
  return compactText(session.lastQuestion || session.firstQuestion || session.lastAnswer || session.agentType || "", "空会话");
}

function eventSummary(event) {
  if (event.type === "tool_start") return `${event.toolName || "tool"} ${event.arguments || ""}`.trim();
  if (event.type === "tool_end") return `${event.toolName || "tool"} 完成`;
  if (event.type === "error") return event.message || event.content || event.code || "error";
  return compactText(event.content || event.message || event.code || JSON.stringify(event.data || {}), "");
}
</script>
