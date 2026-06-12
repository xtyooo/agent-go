const toolCatalog = [
  {
    key: "web_search",
    icon: "◎",
    label: "Web 搜索",
    enabled: true,
    definition: {
      name: "web_search",
      description: "Search public web pages and return concise source-backed results.",
      schema: {
        type: "object",
        properties: {
          query: { type: "string" },
          max_results: { type: "integer", minimum: 1, maximum: 10 }
        },
        required: ["query"]
      }
    }
  },
  {
    key: "web_search_mock",
    icon: ">",
    label: "Web 搜索 Mock",
    enabled: true,
    definition: {
      name: "web_search_mock",
      description: "Return mock web search results for local demos.",
      schema: {
        type: "object",
        properties: {
          query: { type: "string" }
        },
        required: ["query"]
      }
    }
  },
  {
    key: "weather_mock",
    icon: "□",
    label: "天气 Mock",
    enabled: true,
    definition: {
      name: "weather_mock",
      description: "Return mock weather information.",
      schema: {
        type: "object",
        properties: {
          city: { type: "string" }
        },
        required: ["city"]
      }
    }
  },
  {
    key: "current_time",
    icon: "◷",
    label: "当前时间",
    enabled: true,
    definition: {
      name: "current_time",
      description: "Return current local time.",
      schema: {
        type: "object",
        properties: {}
      }
    }
  }
];

const demoSteps = [
  {
    title: "理解用户目标",
    time: "14:32:18",
    status: "success",
    body: "分析项目登录流程，识别关键组件和流程，找出潜在问题并给出优化建议。"
  },
  {
    title: "制定执行计划",
    time: "14:32:25",
    status: "success",
    body: "1. 探索项目结构，定位相关文件\n2. 搜索与登录相关的代码和配置\n3. 分析认证流程和数据流\n4. 识别潜在问题和安全隐患\n5. 提出优化建议"
  },
  {
    title: "调用工具：搜索登录相关资料",
    time: "14:32:45",
    status: "success",
    toolCall: {
      id: "call_search_login",
      index: 0,
      name: "web_search",
      arguments: JSON.stringify({ query: "login auth signin token best practices" }, null, 2)
    },
    result: "结果摘要：找到 5 条相关资料。"
  },
  {
    title: "生成优化建议",
    time: "14:33:15",
    status: "success",
    body: "提供安全加固、性能优化和用户体验改进建议。"
  }
];

const state = {
  selectedTab: "request",
  tools: toolCatalog.map((tool) => ({ ...tool })),
  requestId: "run_demo_idle",
  conversationId: "demo_console",
  eventSource: null,
  startedAt: null,
  events: [],
  steps: demoSteps,
  finalText: "",
  activeToolCalls: new Map()
};

function buildRuntimeRequest() {
  const task = el("taskInput").value.trim();
  const activeTools = state.tools.filter((tool) => tool.enabled).map((tool) => tool.definition);
  const toolCalls = state.steps.filter((step) => step.toolCall).map((step) => step.toolCall);

  return {
    requestId: state.requestId,
    temperature: Number(el("temperature").value),
    maxTokens: 0,
    maxTurns: Number(el("maxTurns").value),
    messages: [
      {
        role: "system",
        content: "你是一个严谨的 Agent，需要按计划调用工具、汇总证据并给出可执行建议。"
      },
      {
        role: "user",
        content: task
      },
      {
        role: "assistant",
        content: state.finalText || "等待模型输出。",
        toolCalls
      }
    ],
    tools: activeTools
  };
}

function buildStreamURL() {
  const apiBase = el("apiBaseUrl").value.trim().replace(/\/+$/, "");
  const params = new URLSearchParams({
    query: el("taskInput").value.trim(),
    conversationId: state.conversationId,
    temperature: el("temperature").value,
    maxTurns: el("maxTurns").value
  });
  return `${apiBase}/agent/chat/stream?${params.toString()}`;
}

function startRun() {
  const query = el("taskInput").value.trim();
  if (!query) {
    setStatus("error", "任务为空");
    return;
  }

  closeStream();
  state.requestId = `run_demo_${Date.now().toString(36)}`;
  state.conversationId = `demo_${Date.now().toString(36)}`;
  state.startedAt = new Date();
  state.events = [];
  state.steps = [];
  state.finalText = "";
  state.activeToolCalls = new Map();

  el("startedAt").textContent = formatDateTime(state.startedAt);
  el("finalOutput").textContent = "模型正在输出...";
  setTaskBadge("pending", "运行中");
  setStatus("running", "运行中");
  renderTimeline();
  renderRuntimePreview();

  const url = buildStreamURL();
  state.eventSource = new EventSource(url);
  state.eventSource.onmessage = (message) => {
    try {
      handleEvent(JSON.parse(message.data));
    } catch (error) {
      appendStep({
        title: "事件解析失败",
        status: "error",
        body: String(error),
        result: message.data
      });
      setStatus("error", "解析失败");
    }
  };
  state.eventSource.onerror = () => {
    if (state.eventSource) {
      appendStep({
        title: "SSE 连接异常",
        status: "error",
        body: "请确认后端服务地址、CORS 与 /agent/chat/stream 是否可访问。"
      });
      setStatus("error", "连接异常");
      setTaskBadge("error", "异常");
      closeStream();
      renderRuntimePreview();
    }
  };
}

function pauseRun() {
  if (!state.eventSource) {
    return;
  }
  closeStream();
  appendStep({
    title: "用户暂停运行",
    status: "pending",
    body: "SSE 连接已关闭，后端请求会随 HTTP 连接断开而取消。"
  });
  setStatus("idle", "已暂停");
  setTaskBadge("pending", "已暂停");
  renderRuntimePreview();
}

function resetRun() {
  closeStream();
  el("taskInput").value = "分析项目中的登录流程，并给出可优化建议。";
  el("temperature").value = "0.2";
  el("maxTurns").value = "10";
  el("temperatureValue").textContent = "0.2";
  el("maxTurnsValue").textContent = "10";
  state.tools = toolCatalog.map((tool) => ({ ...tool }));
  state.requestId = "run_demo_idle";
  state.conversationId = "demo_console";
  state.startedAt = null;
  state.events = [];
  state.steps = demoSteps;
  state.finalText = "";
  state.activeToolCalls = new Map();
  el("startedAt").textContent = "未开始";
  el("finalOutput").textContent = "点击“开始运行”后，这里会实时累积模型文本输出。";
  setStatus("idle", "空闲");
  setTaskBadge("pending", "待运行");
  renderToolToggles();
  syncTask();
  renderTimeline();
}

function handleEvent(evt) {
  state.events.push(evt);

  switch (evt.type) {
    case "thinking":
      appendStep({
        title: "Agent 思考",
        status: "running",
        body: evt.content || "正在分析..."
      });
      break;
    case "text":
      state.finalText += evt.content || "";
      el("finalOutput").textContent = state.finalText || "模型正在输出...";
      break;
    case "tool_start":
      state.activeToolCalls.set(evt.toolCallId || evt.toolName, state.steps.length);
      appendStep({
        title: `调用工具：${evt.toolName || "unknown"}`,
        status: "running",
        toolCall: {
          id: evt.toolCallId || "",
          index: state.activeToolCalls.size - 1,
          name: evt.toolName || "",
          arguments: evt.arguments || "{}"
        }
      });
      break;
    case "tool_end":
      finishToolStep(evt);
      break;
    case "reference":
      appendStep({
        title: "引用资料",
        status: "success",
        body: evt.content || "",
        result: evt.count ? `共 ${evt.count} 条引用` : ""
      });
      break;
    case "recommend":
      appendStep({
        title: "推荐问题",
        status: "success",
        body: evt.content || "",
        result: evt.data ? JSON.stringify(evt.data, null, 2) : ""
      });
      break;
    case "error":
      appendStep({
        title: `运行错误：${evt.code || "ERROR"}`,
        status: "error",
        body: evt.message || evt.content || "Agent 返回错误",
        result: evt.detail || ""
      });
      setStatus("error", "运行错误");
      setTaskBadge("error", "异常");
      closeStream();
      break;
    case "complete":
      setStatus("idle", "已完成");
      setTaskBadge("success", "已完成");
      closeStream();
      break;
    default:
      appendStep({
        title: `事件：${evt.type || "unknown"}`,
        status: "success",
        body: evt.content || JSON.stringify(evt, null, 2)
      });
  }

  renderTimeline();
  renderRuntimePreview();
}

function finishToolStep(evt) {
  const key = evt.toolCallId || evt.toolName;
  const index = state.activeToolCalls.get(key);
  if (index === undefined || !state.steps[index]) {
    appendStep({
      title: `工具完成：${evt.toolName || "unknown"}`,
      status: "success",
      result: evt.result || evt.content || ""
    });
    return;
  }
  state.steps[index] = {
    ...state.steps[index],
    status: "success",
    result: evt.result || evt.content || "工具执行完成"
  };
}

function appendStep(step) {
  state.steps.push({
    time: formatTime(new Date()),
    ...step
  });
}

function renderToolToggles() {
  const container = el("toolToggles");
  container.innerHTML = "";

  state.tools.forEach((tool) => {
    const row = document.createElement("label");
    row.className = "tool-toggle";
    row.innerHTML = `
      <span class="tool-name">
        <span class="tool-icon">${tool.icon}</span>
        <span>${tool.label}</span>
      </span>
      <span class="switch">
        <input type="checkbox" ${tool.enabled ? "checked" : ""} data-tool-key="${tool.key}">
        <span class="slider"></span>
      </span>
    `;
    container.appendChild(row);
  });

  container.querySelectorAll("input[type='checkbox']").forEach((input) => {
    input.addEventListener("change", (event) => {
      const tool = state.tools.find((item) => item.key === event.target.dataset.toolKey);
      tool.enabled = event.target.checked;
      renderRuntimePreview();
    });
  });
}

function renderTimeline() {
  const container = el("timeline");
  container.innerHTML = "";

  state.steps.forEach((step, index) => {
    const item = document.createElement("article");
    item.className = `timeline-item ${statusClass(step.status)}`;
    item.innerHTML = `
      <span class="step-index">${index + 1}</span>
      <span class="step-time">${escapeHtml(step.time || "")}</span>
      <div class="step-card">
        <div class="step-main">
          <div>
            <h3>${escapeHtml(step.title)}</h3>
            ${step.body ? `<p>${escapeHtml(step.body).replaceAll("\n", "<br>")}</p>` : ""}
          </div>
          <span class="badge ${badgeClass(step.status)}">${statusText(step.status)}</span>
          <button class="step-toggle" type="button" aria-label="展开或收起">⌄</button>
        </div>
        ${step.toolCall ? `<pre class="step-code">${escapeHtml(formatToolCall(step.toolCall))}</pre>` : ""}
        ${step.result ? `<p class="result-summary ${step.status === "pending" ? "warning" : ""}">${escapeHtml(step.result)}</p>` : ""}
      </div>
    `;
    item.querySelector(".step-toggle").addEventListener("click", () => {
      item.classList.toggle("is-collapsed");
    });
    container.appendChild(item);
  });
}

function renderRuntimePreview() {
  const request = buildRuntimeRequest();
  const view = {
    request,
    messages: request.messages,
    tools: request.tools
  }[state.selectedTab];

  el("runtimePreview").textContent = JSON.stringify(view, null, 2);
  el("turnCount").textContent = String(Math.max(state.steps.length, state.events.length));
  el("metricTurns").textContent = String(Math.max(state.steps.length, state.events.length));
  el("runId").textContent = request.requestId;
}

function bindInputs() {
  el("taskInput").addEventListener("input", syncTask);
  el("apiBaseUrl").addEventListener("input", renderRuntimePreview);
  el("temperature").addEventListener("input", () => {
    el("temperatureValue").textContent = el("temperature").value;
    renderRuntimePreview();
  });
  el("maxTurns").addEventListener("input", () => {
    el("maxTurnsValue").textContent = el("maxTurns").value;
    renderRuntimePreview();
  });
  el("startButton").addEventListener("click", startRun);
  el("pauseButton").addEventListener("click", pauseRun);
  el("resetButton").addEventListener("click", resetRun);

  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      state.selectedTab = tab.dataset.tab;
      document.querySelectorAll(".tab").forEach((item) => item.classList.remove("is-active"));
      tab.classList.add("is-active");
      renderRuntimePreview();
    });
  });
}

function syncTask() {
  const task = el("taskInput").value.trim();
  el("taskCount").textContent = String(el("taskInput").value.length);
  el("headerTask").textContent = task || "未填写任务";
  el("taskTitle").textContent = task || "未填写任务";
  renderRuntimePreview();
}

function closeStream() {
  if (state.eventSource) {
    state.eventSource.close();
    state.eventSource = null;
  }
}

function setStatus(kind, text) {
  const node = el("runStatus");
  node.className = `status-pill ${kind}`;
  node.innerHTML = `<span class="status-dot"></span>${escapeHtml(text)}`;
}

function setTaskBadge(kind, text) {
  const node = el("taskBadge");
  node.className = `badge ${kind}`;
  node.textContent = text;
}

function formatToolCall(toolCall) {
  return `${toolCall.name}(${toolCall.arguments || "{}"})`;
}

function statusClass(status) {
  if (status === "error") return "error";
  if (status === "running") return "running";
  if (status === "pending") return "need-confirm";
  return "done";
}

function badgeClass(status) {
  if (status === "error") return "error";
  if (status === "pending" || status === "running") return "pending";
  return "success";
}

function statusText(status) {
  if (status === "error") return "失败";
  if (status === "running") return "执行中";
  if (status === "pending") return "需确认";
  return "成功";
}

function formatDateTime(date) {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${formatTime(date)}`;
}

function formatTime(date) {
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function pad(value) {
  return String(value).padStart(2, "0");
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function el(id) {
  return document.getElementById(id);
}

renderToolToggles();
renderTimeline();
bindInputs();
syncTask();
