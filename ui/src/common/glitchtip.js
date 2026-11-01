// Error reports to GlitchTip (Sentry protocol), no SDK. The server hands out
// the DSN at /ui/api/glitchtip (GLITCHTIP_DSN; empty = off). Catches what the
// ErrorBoundary catches plus uncaught errors and unhandled promise rejections.
const MAX_EVENTS = 10; // per page load
const MAX_TRAIL = 20;

let config = null; // { url, release } once loaded; false = off
let queue = []; // events captured before the config arrived
let sent = 0;
const trail = []; // recent navigations, newest last
const seen = new Set();

// "http://key@host/7" -> envelope URL with the key in the query string, which
// with a text/plain body keeps the POST a simple CORS request (no preflight).
export function envelopeUrl(dsn) {
  const m = /^(https?):\/\/([^@]+)@([^/]+)\/(.+)$/.exec(dsn || "");
  if (!m) return null;
  const [, scheme, key, host, project] = m;
  return `${scheme}://${host}/api/${project}/envelope/?sentry_key=${key}&sentry_version=7`;
}

// Chrome: "    at fn (url:1:2)" / "    at url:1:2"; Firefox/Safari: "fn@url:1:2".
// Sentry wants the oldest frame first.
export function parseStack(stack) {
  const frames = [];
  for (const line of (stack || "").split("\n")) {
    const m = /^\s*at (?:(.*?) \()?(.+?):(\d+):(\d+)\)?\s*$/.exec(line) ||
      /^\s*(.*?)@(.+?):(\d+):(\d+)\s*$/.exec(line);
    if (!m) continue;
    frames.push({
      function: m[1] || "?",
      filename: m[2],
      abs_path: m[2],
      lineno: Number(m[3]),
      colno: Number(m[4]),
      in_app: !/\/(vendor|react|pdf)-[^/]*\.js/.test(m[2]),
    });
  }
  return frames.reverse();
}

// crypto.randomUUID needs a secure context; remarkable.svc is plain http.
function eventId() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, "0")).join("");
}

export function buildEvent(error, { release, url, userAgent, trail = [], extra = {}, now = new Date() } = {}) {
  const err = error instanceof Error ? error : new Error(String(error));
  const frames = parseStack(err.stack);
  return {
    event_id: eventId(),
    timestamp: now.toISOString(),
    platform: "javascript",
    level: "error",
    logger: "rmfakecloud-ui",
    release: release || undefined,
    request: { url, headers: { "User-Agent": userAgent } },
    exception: { values: [{ type: err.name, value: err.message, stacktrace: frames.length ? { frames } : undefined }] },
    breadcrumbs: { values: trail.map((t) => ({ timestamp: t.timestamp, category: "navigation", message: t.path })) },
    extra,
  };
}

export function envelope(event) {
  return [
    JSON.stringify({ event_id: event.event_id, sent_at: event.timestamp }),
    JSON.stringify({ type: "event" }),
    JSON.stringify(event),
  ].join("\n");
}

function send(event) {
  if (sent >= MAX_EVENTS) return;
  sent++;
  fetch(config.url, {
    method: "POST",
    mode: "no-cors",
    keepalive: true,
    headers: { "Content-Type": "text/plain;charset=UTF-8" },
    body: envelope(event),
  }).catch(() => {});
}

export function captureException(error, extra = {}) {
  if (config === false) return;
  try {
    // React reports one render error several times (dev double render, boundary).
    const key = `${error?.name}:${error?.message}:${location.pathname}`;
    if (seen.has(key)) return;
    seen.add(key);
    const event = buildEvent(error, {
      release: config?.release,
      url: location.href,
      userAgent: navigator.userAgent,
      trail: trail.slice(),
      extra,
    });
    if (config) send(event);
    else if (queue.length < MAX_EVENTS) queue.push(event);
  } catch (e) {
    console.warn("glitchtip: could not report", e); // never break the page
  }
}

function recordNavigation() {
  trail.push({ timestamp: Date.now() / 1000, path: location.pathname + location.search });
  if (trail.length > MAX_TRAIL) trail.shift();
}

export function initGlitchtip() {
  recordNavigation();
  for (const name of ["pushState", "replaceState"]) {
    const orig = history[name];
    history[name] = function (...args) {
      const result = orig.apply(this, args);
      recordNavigation();
      return result;
    };
  }
  window.addEventListener("popstate", recordNavigation);
  window.addEventListener("error", (e) => captureException(e.error || e.message, { source: "window.onerror" }));
  window.addEventListener("unhandledrejection", (e) => captureException(e.reason, { source: "unhandledrejection" }));

  fetch("/ui/api/glitchtip")
    .then((r) => (r.ok ? r.json() : {}))
    .then(({ dsn, release }) => {
      const url = envelopeUrl(dsn);
      config = url ? { url, release } : false;
      if (config) queue.forEach((event) => send({ ...event, release: release || undefined }));
      queue = [];
    })
    .catch(() => { config = false; queue = []; });
}
