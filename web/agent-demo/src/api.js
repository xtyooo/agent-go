const JSON_HEADERS = {
  "Content-Type": "application/json"
};

export const AGENT_OPTIONS = [
  {
    value: "websearch",
    label: "WebSearch",
    description: "适合联网搜索、资料整理和普通问答",
    streamPath: "/agent/chat/stream"
  },
  {
    value: "plan-execute",
    label: "Deep Agent",
    description: "适合复杂任务拆解和多步执行",
    streamPath: "/agent/deep/stream"
  },
  {
    value: "skills",
    label: "Skills",
    description: "适合调用本地技能目录里的能力",
    streamPath: "/agent/skills/stream"
  },
  {
    value: "pptx",
    label: "PPTBuilder",
    description: "适合生成、继续或修改演示文稿",
    streamPath: "/agent/pptx/stream"
  }
];

export function findAgent(value) {
  return AGENT_OPTIONS.find((item) => item.value === value) || AGENT_OPTIONS[0];
}

export async function fetchSessions({ keyword = "", agentType = "", limit = 50, offset = 0 } = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset)
  });
  if (keyword) params.set("keyword", keyword);
  if (agentType) params.set("agentType", agentType);

  const payload = await requestJSON(`/session/list?${params.toString()}`);
  return {
    total: Number(payload.total || 0),
    items: Array.isArray(payload.items) ? payload.items : []
  };
}

export async function fetchSessionDetail(sessionId) {
  const payload = await requestJSON(`/session/detail?sessionId=${encodeURIComponent(sessionId)}`, {
    allowNotFound: true
  });
  return Array.isArray(payload.items) ? payload.items : [];
}

export async function renameSession(sessionId, sessionName) {
  return requestJSON(`/session/${encodeURIComponent(sessionId)}/rename`, {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ sessionName })
  });
}

export async function deleteSession(sessionId) {
  return requestJSON(`/session/${encodeURIComponent(sessionId)}`, {
    method: "DELETE"
  });
}

export async function stopAgent(conversationId) {
  return requestJSON(`/agent/stop?conversationId=${encodeURIComponent(conversationId)}`, {
    method: "POST"
  });
}

export async function fetchTraceDetail(traceId) {
  return requestJSON(`/trace/detail?traceId=${encodeURIComponent(traceId)}`);
}

export async function fetchPPTLatest(conversationId) {
  return requestJSON(`/pptx/latest?conversationId=${encodeURIComponent(conversationId)}`, {
    allowNotFound: true
  });
}

export function buildPPTPreviewURL(pptId) {
  return `/pptx/${encodeURIComponent(pptId)}/preview`;
}

export function buildPPTDownloadURL(pptId) {
  return `/pptx/${encodeURIComponent(pptId)}/download`;
}

export function buildTraceReplayURL({ traceId, originalTiming = false, maxDelayMs = 500 }) {
  const params = new URLSearchParams({
    traceId
  });
  if (originalTiming) {
    params.set("timing", "original");
    params.set("maxDelayMs", String(maxDelayMs));
  }
  return `/trace/replay/stream?${params.toString()}`;
}

export function buildStreamURL({ query, conversationId, agentType, temperature, maxTurns, traceId }) {
  const agent = findAgent(agentType);
  const params = new URLSearchParams({
    query,
    conversationId,
    temperature: String(temperature),
    maxTurns: String(maxTurns)
  });
  if (traceId) params.set("traceId", traceId);
  return `${agent.streamPath}?${params.toString()}`;
}

async function requestJSON(url, options = {}) {
  const { allowNotFound = false, ...fetchOptions } = options;
  const response = await fetch(url, fetchOptions);
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }

  if (allowNotFound && response.status === 404) {
    return payload || {};
  }

  if (!response.ok || payload?.success === false) {
    const message = payload?.message || `${response.status} ${response.statusText}`.trim();
    const error = new Error(message || "请求失败");
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload || {};
}
