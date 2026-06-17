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

      <div v-if="currentUser" class="sidebar-footer">
        <span class="user-dot">{{ currentUser.initials }}</span>
        <span>
          <strong>{{ currentUser.name }}</strong>
          <small>{{ currentUser.plan }}</small>
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
              <div
                v-for="item in message.process"
                :key="item.id"
                class="process-card"
                :class="[item.kind, item.status, { open: isProcessExpanded(item) }]"
              >
                <button
                  class="process-summary"
                  type="button"
                  :aria-expanded="isProcessExpanded(item)"
                  @click="toggleProcess(item)"
                >
                  <span v-if="item.kind !== 'thinking'" :class="['process-dot', item.status]"></span>
                  <span class="process-title">{{ processTitle(item) }}</span>
                  <small v-if="processMeta(item)">{{ processMeta(item) }}</small>
                  <ChevronDown class="process-chevron" :size="14" />
                </button>
                <div v-if="item.detail && isProcessExpanded(item)" class="process-detail">{{ item.detail }}</div>
              </div>
            </div>

            <div v-if="showRunProgress(message)" class="run-progress" role="status">
              <div class="run-progress-orbit" aria-hidden="true">
                <span></span>
                <span></span>
                <span></span>
              </div>
              <div class="run-progress-copy">
                <strong>{{ runningPhase(message).title }}</strong>
                <small>{{ runningPhase(message).detail }}</small>
              </div>
              <div class="run-progress-rail" aria-hidden="true">
                <span :style="{ width: `${runningPhase(message).percent}%` }"></span>
              </div>
            </div>

            <div v-if="message.content" class="markdown-body" v-html="renderMarkdown(message.content)"></div>
            <div v-if="message.role === 'assistant' && pptArtifact(message)" class="artifact-card">
              <div class="artifact-icon">
                <Presentation :size="18" />
              </div>
              <div class="artifact-copy">
                <strong>{{ pptArtifact(message).title }}</strong>
                <small>{{ pptArtifact(message).meta }}</small>
              </div>
              <div class="artifact-actions">
                <button
                  class="artifact-button"
                  type="button"
                  :disabled="pptArtifact(message).previewDisabled"
                  title="在线预览"
                  @click="openPPTPreview(pptArtifact(message))"
                >
                  <Eye :size="15" />
                  <span>预览</span>
                </button>
                <a
                  class="artifact-button primary"
                  :class="{ disabled: pptArtifact(message).downloadDisabled }"
                  :href="pptArtifact(message).downloadUrl || '#'"
                  :aria-disabled="pptArtifact(message).downloadDisabled"
                  :download="pptArtifact(message).downloadFilename"
                  title="下载 PPTX"
                  @click="handlePPTDownloadClick($event, pptArtifact(message))"
                >
                  <Download :size="15" />
                  <span>下载</span>
                </a>
              </div>
            </div>
            <div v-else-if="message.role === 'assistant' && isRunning && !message.content" class="typing">
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
          <div>
            <span>Trace ID</span>
            <strong>{{ currentTraceId || "-" }}</strong>
          </div>
          <div>
            <span>模式</span>
            <strong>{{ runModeLabel }}</strong>
          </div>
        </div>
      </section>

      <section class="settings-section">
        <div class="section-title">
          <label>Trace 回放</label>
          <button class="text-button" type="button" :disabled="!traceIdInput" @click="copyTraceId">复制 ID</button>
        </div>
        <div class="trace-card">
          <label class="trace-field">
            <span>Trace ID</span>
            <input v-model.trim="traceIdInput" type="text" placeholder="粘贴 traceId 或运行后自动填入" />
          </label>
          <div class="trace-actions">
            <button class="tool-button" type="button" :disabled="traceLoading || !traceIdInput" @click="loadTraceDetail">
              <SearchCheck :size="15" />
              查询
            </button>
            <button class="tool-button" type="button" :disabled="isRunning || !traceIdInput" @click="replayTrace(false)">
              <Play :size="15" />
              快速回放
            </button>
            <button class="tool-button" type="button" :disabled="isRunning || !traceIdInput" @click="replayTrace(true)">
              <Clock :size="15" />
              原速
            </button>
          </div>
          <p v-if="traceError" class="trace-error">{{ traceError }}</p>
          <div v-if="traceDetail" class="trace-summary">
            <div>
              <span>Agent</span>
              <strong>{{ traceDetail.agentType || "-" }}</strong>
            </div>
            <div>
              <span>状态</span>
              <strong>{{ traceDetail.status || "-" }}</strong>
            </div>
            <div>
              <span>事件</span>
              <strong>{{ traceDetail.eventCount || 0 }}</strong>
            </div>
            <div>
              <span>耗时</span>
              <strong>{{ formatDuration(traceDetail.elapsedMs) }}</strong>
            </div>
          </div>
          <div v-if="traceDetail" class="trace-query">{{ traceDetail.query || "无查询文本" }}</div>
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

    <div v-if="pptPreview.open" class="modal-layer" role="dialog" aria-modal="true" aria-label="PPT 在线预览">
      <button class="modal-backdrop" type="button" aria-label="关闭 PPT 预览" @click="closePPTPreview"></button>
      <section class="ppt-preview-modal">
        <header class="ppt-preview-header">
          <div>
            <strong>{{ pptPreview.title }}</strong>
            <small>{{ pptPreview.subtitle }}</small>
          </div>
          <div class="ppt-preview-actions">
            <a
              v-if="pptPreview.downloadUrl"
              class="tool-button"
              :href="pptPreview.downloadUrl"
              :download="pptPreview.downloadFilename"
            >
              <Download :size="15" />
              下载 PPTX
            </a>
            <button class="icon-button" type="button" title="关闭预览" @click="closePPTPreview">
              <X :size="18" />
            </button>
          </div>
        </header>
        <iframe v-if="pptPreview.url" class="ppt-preview-frame" :src="pptPreview.url" title="PPT 在线预览"></iframe>
        <div v-else class="ppt-preview-empty">当前 PPT 还没有可预览内容</div>
      </section>
    </div>
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
  Clock,
  Copy,
  Download,
  Eye,
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
  Play,
  Plus,
  Presentation,
  Search,
  SearchCheck,
  SendHorizontal,
  Share2,
  Sparkles,
  Square,
  Trash2,
  UserRound,
  X
} from "@lucide/vue";
import {
  AGENT_OPTIONS,
  buildPPTDownloadURL,
  buildPPTPreviewURL,
  buildTraceReplayURL,
  buildStreamURL,
  deleteSession,
  fetchPPTLatest,
  fetchTraceDetail,
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
  "生成一份 AI Agent 技术分享 PPT，6 页，科技风",
  "回放上一次失败的 Agent trace"
];

const TRACE_ID_PREFIX = "web";

const sidebarOpen = ref(false);
const detailsOpen = ref(false);
const agentPickerOpen = ref(false);
const sessions = ref([]);
const sessionsLoading = ref(false);
const sessionError = ref("");
const currentUser = ref(null);
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
const currentTraceId = ref("");
const traceIdInput = ref("");
const traceDetail = ref(null);
const traceLoading = ref(false);
const traceError = ref("");
const replayingTrace = ref(false);
const pptPreview = ref({
  open: false,
  url: "",
  title: "",
  subtitle: "",
  downloadUrl: "",
  downloadFilename: ""
});
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
const runModeLabel = computed(() => (replayingTrace.value ? "Trace 回放" : "实时运行"));
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
      sessionError.value = sessionErrorMessage(error);
    }
  } finally {
    if (!silent) sessionsLoading.value = false;
  }
}

function sessionErrorMessage(error) {
  if (error?.status === 405) {
    return "后端会话接口未启用";
  }
  if (error?.status === 404) {
    return "后端未提供历史会话";
  }
  return "历史会话暂不可用";
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
    currentTraceId.value = "";
    traceDetail.value = null;
    traceError.value = "";
    sidebarOpen.value = false;
    hydratePPTArtifactForCurrentSession();
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
      kind: "thinking",
      title: "已思考",
      status: "success",
      meta: "已完成",
      detail: record.thinking
    });
  }
  if (record.tools) {
    process.push({
      id: `tools-${record.id}`,
      kind: "tool",
      title: "工具调用",
      status: "success",
      meta: "已完成",
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
  closePPTPreview();
  activeConversationId.value = "";
  activeSessionTitle.value = "";
  messages.value = [];
  streamEvents.value = [];
  errorText.value = "";
  firstResponseMs.value = 0;
  currentTraceId.value = "";
  traceDetail.value = null;
  traceError.value = "";
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

function isProcessExpanded(item) {
  return item.expanded ?? item.status === "running";
}

function toggleProcess(item) {
  item.expanded = !isProcessExpanded(item);
}

function processTitle(item) {
  if (item.kind === "thinking") {
    return item.status === "running" ? "思考中" : "已思考";
  }
  return item.title;
}

function processMeta(item) {
  if (item.kind === "thinking") {
    return "";
  }
  return item.meta;
}

async function sendMessage() {
  const query = draft.value.trim();
  if (!query || isRunning.value) return;

  closeStream();
  errorText.value = "";
  const conversationId = activeConversationId.value || createConversationId();
  const traceId = createTraceId();
  activeConversationId.value = conversationId;
  currentTraceId.value = traceId;
  traceIdInput.value = traceId;
  traceDetail.value = null;
  traceError.value = "";
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
  replayingTrace.value = false;
  runStartedAt.value = performance.now();
  firstResponseMs.value = 0;
  streamEvents.value = [];
  pendingToolCallIds.clear();

  const url = buildStreamURL({
    query,
    conversationId,
    agentType: agentType.value,
    temperature: temperature.value,
    maxTurns: maxTurns.value,
    traceId
  });

  openEventStream(url, "SSE 连接异常，请确认 Go 服务和模型配置可用");
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
    appendThinkingProcess(assistant, typedEvent);
    return;
  }

  if (event.type === "tool_start") {
    const processId = event.toolCallId || typedEvent.id;
    if (event.toolName) {
      pendingToolCallIds.set(event.toolName, processId);
    }
    assistant.process.push({
      id: processId,
      kind: "tool",
      title: `使用工具：${event.toolName || "unknown"}`,
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
        kind: "tool",
        title: `使用工具：${event.toolName || "unknown"}`,
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
      kind: "reference",
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
      kind: "recommend",
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

function appendThinkingProcess(assistant, event) {
  const content = event.content || "";
  if (!content) return;

  let item = assistant.process.find((process) => process.kind === "thinking");
  if (!item) {
    item = {
      id: `thinking-${assistant.id}`,
      kind: "thinking",
      title: "思考中",
      status: "running",
      meta: "正在运行",
      detail: ""
    };
    assistant.process.unshift(item);
  }

  item.status = "running";
  item.meta = "正在运行";
  item.detail = appendProcessDetail(item.detail, content);
  assistant.runningPhase = inferRunningPhase(assistant);
}

function appendProcessDetail(current, next) {
  if (!current) return next.trimStart();
  return current + next;
}

function openEventStream(url, errorMessage) {
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
      failRun(errorMessage);
    }
  };
}

async function loadTraceDetail() {
  const traceId = traceIdInput.value.trim();
  if (!traceId) return;
  traceLoading.value = true;
  traceError.value = "";
  try {
    traceDetail.value = await fetchTraceDetail(traceId);
    currentTraceId.value = traceDetail.value.traceId || traceId;
  } catch (error) {
    traceDetail.value = null;
    traceError.value = error.message || "Trace 查询失败";
  } finally {
    traceLoading.value = false;
  }
}

async function replayTrace(originalTiming = false) {
  const traceId = traceIdInput.value.trim();
  if (!traceId || isRunning.value) return;

  closeStream();
  errorText.value = "";
  traceError.value = "";
  currentTraceId.value = traceId;
  if (!traceDetail.value || traceDetail.value.traceId !== traceId) {
    try {
      traceDetail.value = await fetchTraceDetail(traceId);
    } catch (error) {
      traceDetail.value = null;
      traceError.value = error.message || "Trace 查询失败";
      return;
    }
  }

  const run = traceDetail.value || {};
  activeConversationId.value = run.conversationId || `trace_${traceId}`;
  activeSessionTitle.value = compactText(run.query, `Trace ${traceId}`);
  agentType.value = run.agentType || agentType.value;
  messages.value = [
    {
      id: `trace-user-${traceId}`,
      role: "user",
      content: run.query || `Replay trace ${traceId}`,
      createdAt: run.startedAt || new Date().toISOString()
    },
    {
      id: `trace-assistant-${traceId}`,
      role: "assistant",
      content: "",
      createdAt: run.startedAt || new Date().toISOString(),
      process: [],
      references: []
    }
  ];
  currentAssistantId.value = `trace-assistant-${traceId}`;
  streamEvents.value = [];
  pendingToolCallIds.clear();
  firstResponseMs.value = 0;
  runStartedAt.value = performance.now();
  isRunning.value = true;
  replayingTrace.value = true;
  detailsOpen.value = false;

  openEventStream(
    buildTraceReplayURL({ traceId, originalTiming }),
    "Trace 回放连接异常，请确认 trace 文件仍然存在"
  );
}

async function stopRun() {
  if (!isRunning.value || !activeConversationId.value) return;
  if (replayingTrace.value) {
    finishRun();
    return;
  }
  try {
    await stopAgent(activeConversationId.value);
  } catch (error) {
    errorText.value = error.message || "停止失败";
  } finally {
    finishRun();
  }
}

function finishRun(processStatus = "success") {
  const shouldHydratePPT = processStatus === "success" && !replayingTrace.value && agentType.value === "pptx";
  settleRunningProcesses(processStatus);
  closeStream();
  isRunning.value = false;
  replayingTrace.value = false;
  currentAssistantId.value = "";
  pendingToolCallIds.clear();
  loadSessions({ silent: true });
  if (shouldHydratePPT) {
    hydratePPTArtifactForCurrentSession();
  }
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

function createTraceId() {
  const stamp = Date.now().toString(36);
  const random = Math.random().toString(36).slice(2, 10);
  return `${TRACE_ID_PREFIX}_${stamp}_${random}`;
}

function showRunProgress(message) {
  return message.role === "assistant" && isRunning.value && message.id === currentAssistantId.value;
}

function runningPhase(message) {
  return message.runningPhase || inferRunningPhase(message);
}

function inferRunningPhase(message) {
  const detail = message?.process?.find((item) => item.kind === "thinking")?.detail || "";
  const isPPT = agentType.value === "pptx";
  if (isPPT) {
    const stages = [
      { match: ["分析", "需求"], title: "正在分析 PPT 需求", detail: "整理主题、页数、受众与风格", percent: 16 },
      { match: ["收集", "搜索", "资料"], title: "正在收集资料", detail: "为页面内容补充上下文", percent: 32 },
      { match: ["模板"], title: "正在选择模板", detail: "匹配演示文稿的视觉结构", percent: 48 },
      { match: ["大纲"], title: "正在生成大纲", detail: "拆分页面顺序与叙事节奏", percent: 64 },
      { match: ["Schema", "内容设计", "设计"], title: "正在设计页面内容", detail: "把大纲转成可预览的幻灯片结构", percent: 78 },
      { match: ["渲染"], title: "正在渲染 PPT", detail: "生成预览与下载产物", percent: 90 },
      { match: ["总结"], title: "正在整理结果", detail: "准备最后的说明和产物入口", percent: 96 }
    ];
    const matched = stages.find((stage) => stage.match.some((word) => detail.includes(word)));
    if (matched) return matched;
    return { title: "正在生成 PPT", detail: "PPTBuilder 正在推进生成流程", percent: 12 };
  }
  if (message?.process?.some((item) => item.kind === "tool" && item.status === "running")) {
    return { title: "正在使用工具", detail: "工具返回后会继续整理回答", percent: 58 };
  }
  if (detail) {
    return { title: "正在思考", detail: compactText(detail.split("\n").filter(Boolean).at(-1), "正在整理上下文"), percent: 38 };
  }
  if (message?.content) {
    return { title: "正在生成回答", detail: "内容会持续写入当前消息", percent: 74 };
  }
  return { title: "正在连接 Agent", detail: "已发送请求，等待首个事件", percent: 18 };
}

function pptArtifact(message) {
  if (message?.pptArtifact) return normalizePPTArtifact(message.pptArtifact);
  const match = String(message?.content || "").match(/(?:mock|https?|file):\/\/[^\s)）\]]+\.pptx|mock:\/\/ppt\/[^\s)）\]]+/i);
  if (!match && agentType.value !== "pptx") return null;
  const url = match?.[0]?.replace(/[。,.，]+$/, "") || "";
  if (!url && !message?.pptArtifact) return null;
  return {
    id: "",
    title: "PPT 产物",
    url,
    meta: url ? (url.startsWith("mock://") ? "后端已生成基础 PPTX 下载入口" : url) : "正在准备产物入口",
    previewUrl: "",
    downloadUrl: /^https?:\/\//i.test(url) || /^file:\/\//i.test(url) ? url : "",
    downloadFilename: "kimo-agent.pptx",
    previewDisabled: true,
    downloadDisabled: !(/^https?:\/\//i.test(url) || /^file:\/\//i.test(url))
  };
}

function normalizePPTArtifact(artifact) {
  const id = artifact.id || "";
  const pageCount = Number(artifact.pageCount || artifact.slides?.length || 0);
  const title = artifact.title || "PPT 产物";
  const downloadUrl = artifact.downloadUrl || (id ? buildPPTDownloadURL(id) : "");
  const previewUrl = artifact.previewUrl || (id ? buildPPTPreviewURL(id) : "");
  const rendererLabel = artifact.rendererStatus === "basic-pptx" ? "基础 PPTX" : "PPTX";
  return {
    id,
    title,
    url: artifact.fileUrl || artifact.url || "",
    meta: artifact.meta || `${pageCount || "-"} 页 · ${rendererLabel} 可下载 · 支持在线预览`,
    pageCount,
    previewUrl,
    downloadUrl,
    downloadFilename: `kimo-agent-ppt-${id || Date.now()}.pptx`,
    previewDisabled: !previewUrl,
    downloadDisabled: !downloadUrl
  };
}

async function hydratePPTArtifactForCurrentSession() {
  const conversationId = activeConversationId.value;
  if (!conversationId) return;
  try {
    const payload = await fetchPPTLatest(conversationId);
    if (!payload?.success || !payload.id) return;
    const target = [...messages.value].reverse().find((item) => item.role === "assistant");
    if (!target) return;
    target.pptArtifact = {
      id: payload.id,
      title: "PPT 产物",
      pageCount: payload.pageCount,
      previewUrl: payload.previewUrl,
      downloadUrl: payload.downloadUrl,
      rendererStatus: payload.rendererStatus,
      fileUrl: payload.fileUrl,
      slides: payload.slides
    };
  } catch {
    // 历史会话里没有 PPT 实例时不打扰主聊天流程。
  }
}

function openPPTPreview(artifact) {
  if (!artifact || artifact.previewDisabled) return;
  pptPreview.value = {
    open: true,
    url: artifact.previewUrl,
    title: artifact.title || "PPT 在线预览",
    subtitle: artifact.meta || "",
    downloadUrl: artifact.downloadUrl || "",
    downloadFilename: artifact.downloadFilename || "kimo-agent.pptx"
  };
}

function closePPTPreview() {
  pptPreview.value = {
    open: false,
    url: "",
    title: "",
    subtitle: "",
    downloadUrl: "",
    downloadFilename: ""
  };
}

function handlePPTDownloadClick(event, artifact) {
  if (!artifact || artifact.downloadDisabled) {
    event.preventDefault();
    errorText.value = "PPT 下载入口暂不可用，请等生成完成后再试";
  }
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

async function copyTraceId() {
  if (!traceIdInput.value) return;
  try {
    await navigator.clipboard.writeText(traceIdInput.value);
  } catch (error) {
    traceError.value = "复制失败，请手动选择 Trace ID";
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
  if (event.type === "complete") return "完成";
  return compactText(event.content || event.message || event.code || JSON.stringify(event.data || {}), "");
}
</script>
