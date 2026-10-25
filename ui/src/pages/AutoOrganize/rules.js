// Rule vocabulary for the auto-organize page. The server is the authority on
// validation; these helpers only build sentences and sensible defaults.

export const ROOT_ID = "";
export const NO_AUTOMATION_TAG = "no automation";

export const TRIGGERS = [
  { type: "finished_reading", label: "has been read to the last page", pageBased: true },
  { type: "read_progress", label: "has been read at least X% of the way", pageBased: true },
  { type: "not_opened_for_days", label: "hasn't been opened for N days" },
  { type: "not_modified_for_days", label: "hasn't been modified for N days" },
  { type: "name_matches", label: "has a name matching a pattern" },
  { type: "tag_is", label: "is tagged …" },
  { type: "age_since_created", label: "arrived at least N days ago" },
  { type: "pinned_is", label: "is pinned / is not pinned" },
];

// not built in v1; listed disabled so the catalogue is visible
export const LATER_TRIGGERS = {
  "coming in v2": ["has at least N annotated pages (v2)", "has been in this folder for N days (v2)"],
  later: ["folder holds more than N documents → move oldest (later)", "has the same name as a document in … (later)"],
};

export const TRIGGER_DEFAULTS = {
  finished_reading: { minPages: 3, settleHours: 6 },
  read_progress: { percent: 80, settleHours: 6 },
  not_opened_for_days: { days: 30, minAgeEnabled: true, minAgeDays: 7 },
  not_modified_for_days: { days: 90 },
  name_matches: { pattern: "" },
  tag_is: { tag: "" },
  age_since_created: { days: 14 },
  pinned_is: { pinned: true },
};

export const INTERVALS = [5, 10, 15, 30, 60];

export function isPageBased(type) {
  return type === "finished_reading" || type === "read_progress";
}

export function makeTrigger(type) {
  return { type, settleHours: 6, minAgeEnabled: true, pinned: true, ...TRIGGER_DEFAULTS[type] };
}

export function triggerPhrase(t) {
  if (!t) return "";
  switch (t.type) {
    case "finished_reading":
      return `finished reading, at least ${t.minPages} pages, settled ${t.settleHours} h`;
    case "read_progress":
      return `read at least ${t.percent}%, settled ${t.settleHours} h`;
    case "not_opened_for_days":
      return `not opened for ${t.days} days` + (t.minAgeEnabled ? `, at least ${t.minAgeDays} days old` : "");
    case "not_modified_for_days":
      return `not modified for ${t.days} days`;
    case "name_matches":
      return `name matches “${t.pattern}”`;
    case "tag_is":
      return `tagged “${t.tag}”`;
    case "age_since_created":
      return `arrived at least ${t.days} days ago`;
    case "pinned_is":
      return t.pinned ? "is pinned" : "is not pinned";
    default:
      return t.type;
  }
}

export function folderPath(folders, id) {
  const f = (folders || []).find((x) => x.id === id);
  return f ? f.path : null;
}

// destination display: a picked folder, or "Source / Name" for a new subfolder
export function destinationLabel(rule, folders, resolvedName) {
  if (resolvedName) return resolvedName;
  const cm = rule.destination && rule.destination.createIfMissing;
  if (cm) {
    const parentId = cm.parent === "source" ? rule.source.id : cm.parent;
    const parent = folderPath(folders, parentId);
    return parentId === ROOT_ID || !parent ? cm.name : `${parent} / ${cm.name}`;
  }
  return folderPath(folders, rule.destination.id) || rule.destination.name || "?";
}

export function sourceLabel(rule, folders, resolvedName) {
  return resolvedName || folderPath(folders, rule.source.id) || rule.source.name || "?";
}

export function ruleSentence(rule, folders) {
  return `${sourceLabel(rule, folders)} → ${triggerPhrase(rule.trigger)} → ${destinationLabel(rule, folders)}`;
}

function lastSegment(path) {
  if (!path) return "";
  const i = path.lastIndexOf(" / ");
  return i >= 0 ? path.slice(i + 3) : path;
}

export function newRule(folders) {
  const first = (folders || []).find((f) => f.id !== ROOT_ID);
  return {
    id: "",
    name: "",
    enabled: true,
    source: { id: first ? first.id : ROOT_ID, name: first ? first.name : "My files" },
    scope: "pdf_epub",
    trigger: makeTrigger("finished_reading"),
    destination: { id: ROOT_ID, name: "" },
    action: { type: "move" },
  };
}

// the seed rule: Research Reports + finished reading (at least 3 pages) -> Research Reports/Read
export function exampleRule(folders) {
  const rr = (folders || []).find((f) => f.name.toLowerCase() === "research reports");
  const rule = newRule(folders);
  if (rr) rule.source = { id: rr.id, name: rr.name };
  rule.trigger = makeTrigger("finished_reading");
  rule.destination = { id: "", name: "", createIfMissing: { name: "Read", parent: "source" } };
  return rule;
}

// cache display names next to the UUIDs (the UUID stays the only key)
export function withDisplayNames(rule, folders) {
  const r = JSON.parse(JSON.stringify(rule));
  const src = (folders || []).find((f) => f.id === r.source.id);
  if (src) r.source.name = r.source.id === ROOT_ID ? "My files" : lastSegment(src.path);
  if (!r.destination.createIfMissing) {
    const dst = (folders || []).find((f) => f.id === r.destination.id);
    if (dst) r.destination.name = r.destination.id === ROOT_ID ? "My files" : lastSegment(dst.path);
  }
  return r;
}

export const SKIP_LABELS = {
  in_trash: "in Trash",
  content_unreadable: "content unreadable",
  no_automation: "no-automation tag",
  pinned: "pinned",
  already_in_destination: "already there",
  out_of_scope: "out of scope",
  page_count_unknown: "page count unknown",
  time_unknown: "time unknown",
  held: "held",
  cycle_guard: "cycle guard",
  claimed: "claimed",
  invalid_rule: "invalid rule",
};

export const SKIP_CHIP = {
  no_automation: "tag",
  held: "warn",
  cycle_guard: "warn",
  content_unreadable: "bad",
  invalid_rule: "bad",
};

export function fmtTime(ms) {
  if (!ms) return "";
  const d = new Date(ms);
  const now = new Date();
  const time = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  if (d.toDateString() === now.toDateString()) return time;
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return `Yesterday ${time}`;
  return `${d.toLocaleDateString([], { month: "short", day: "numeric" })}, ${time}`;
}

export function fmtAgo(ms, now = Date.now()) {
  const s = Math.max(0, (now - ms) / 1000);
  if (s < 90) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

// client-side check only; the server (RE2) has the final word on save
export function patternError(pattern) {
  const p = (pattern || "").trim();
  if (!p) return "Enter a name pattern.";
  if (p.length >= 2 && p.startsWith("/") && p.endsWith("/")) {
    try {
      new RegExp(p.slice(1, -1), "i");
    } catch (e) {
      return "Invalid regular expression.";
    }
  }
  return null;
}
