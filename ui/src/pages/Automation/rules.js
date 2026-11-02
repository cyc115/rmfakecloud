// Rule vocabulary for the Automation page. The server is the authority on
// validation; these helpers build the rule sentences, the editor's card
// catalogue and sensible defaults. The stored format is today's auto-organize
// rule: a document trigger + Move is a move rule, a page trigger + Extract
// tasks / Just save notes is a pipeline rule.

export const ROOT_ID = "";
export const ROOT_NAME = "My files";
export const NO_AUTOMATION_TAG = "no automation";

export const PAGE_CHANGED = "page_changed";
export const PAGE_TAGGED = "page_tagged";

// The IF cards. group "page" triggers make pipeline rules, "document"
// triggers make move rules. pdfOnly triggers read the page you stopped on,
// which only PDFs and books report, so their condition is fixed.
export const TRIGGER_CARDS = [
  { type: PAGE_CHANGED, group: "page", icon: "✍️", title: "I write on a page", sub: "new or changed handwriting" },
  { type: PAGE_TAGGED, group: "page", icon: "🏷️", title: "I tag a page", sub: "with a page tag you choose" },
  { type: "finished_reading", group: "document", icon: "📖", title: "I finish reading", sub: "read to the last page", pdfOnly: true },
  { type: "read_progress", group: "document", icon: "📊", title: "I've read X%", sub: "part of the way through", pdfOnly: true },
  { type: "not_opened_for_days", group: "document", icon: "💤", title: "It sits unopened", sub: "for N days" },
  { type: "not_modified_for_days", group: "document", icon: "🕰️", title: "It sits unchanged", sub: "no edits for N days" },
  { type: "age_since_created", group: "document", icon: "📥", title: "It arrives", sub: "N days after it lands" },
  { type: "tag_is", group: "document", icon: "🔖", title: "I tag a document", sub: "with a tag you choose" },
  { type: "name_matches", group: "document", icon: "🔤", title: "Its name matches", sub: "a pattern like WSJ*" },
  { type: "pinned_is", group: "document", icon: "📌", title: "It's pinned / unpinned", sub: "pinned or not" },
];

export const PDF_ONLY_BADGE = "PDFs & books";

// The THEN cards. "for" is the trigger group the action works on.
export const ACTION_CARDS = [
  { key: "extract", for: "page", icon: "✅", title: "Extract tasks to my inbox", sub: "+ save the page to its note" },
  { key: "notes", for: "page", icon: "📝", title: "Just save notes", sub: "transcribe, no tasks" },
  { key: "move", for: "document", icon: "📂", title: "Move it to…", sub: "works on documents" },
];

// the conditions "+ add a condition" offers: the rule's scope
export const CONDITIONS = [
  { scope: "notebooks", label: "only notebooks" },
  { scope: "pdf_epub", label: "only PDFs & books" },
];

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

export function triggerCard(type) {
  return TRIGGER_CARDS.find((c) => c.type === type);
}

export function isPageTrigger(type) {
  return type === PAGE_CHANGED || type === PAGE_TAGGED;
}

// isPdfOnly the trigger works on PDFs & books only (scope locked to pdf_epub)
export function isPdfOnly(type) {
  return type === "finished_reading" || type === "read_progress";
}

export function makeTrigger(type) {
  if (type === PAGE_CHANGED) return { type };
  if (type === PAGE_TAGGED) return { type, tag: "" };
  return { type, settleHours: 6, minAgeEnabled: true, pinned: true, ...TRIGGER_DEFAULTS[type] };
}

export function isPipeline(rule) {
  return !!rule && !!rule.action && rule.action.type === "pipeline";
}

// actionKey the THEN card of a rule
export function actionKey(rule) {
  if (!isPipeline(rule)) return "move";
  return rule.action.tasks === false ? "notes" : "extract";
}

// actionFit whether an action card fits the trigger, and why not
export function actionFit(key, triggerType) {
  const page = isPageTrigger(triggerType);
  if (key === "move" && page) {
    return { ok: false, reason: "Move works on whole documents, so it can't follow a page trigger. Pick a trigger under “On a document” to move things." };
  }
  if (key !== "move" && !page) {
    return { ok: false, reason: "Works on pages: pick “I write on a page” or “I tag a page” to extract tasks or save notes." };
  }
  return { ok: true, reason: "" };
}

// conditionLock the reason the condition is fixed, or "" when it is free
export function conditionLock(triggerType) {
  if (!isPdfOnly(triggerType)) return "";
  const card = triggerCard(triggerType);
  return `Fixed: “${card.title}” uses the page you stopped on, which only PDFs & books report.`;
}

function lastSegment(path) {
  if (!path) return "";
  const i = path.lastIndexOf(" / ");
  return i >= 0 ? path.slice(i + 3) : path;
}

// ctx {folders, watchables} — the live tree, so renamed places read right;
// the names cached on the rule are the fallback
function folderName(ctx, id, cached) {
  if (id === ROOT_ID) return ROOT_NAME;
  const f = ((ctx && ctx.folders) || []).find((x) => x.id === id);
  return f ? f.name || lastSegment(f.path) : cached || "?";
}

function watchName(ctx, w) {
  if (w.kind === "folder" && w.id === ROOT_ID) return ROOT_NAME;
  const hit = ((ctx && ctx.watchables) || []).find((i) => i.id === w.id && i.kind === w.kind);
  return hit ? hit.name : w.name || w.id;
}

export function isRootWatch(w) {
  return w.kind === "folder" && w.id === ROOT_ID;
}

// isAnywhere the rule's page places cover the whole tree: no entry, or the
// root folder among them (a root entry next to others still means anywhere)
export function isAnywhere(rule) {
  const watch = rule.watch || [];
  return watch.length === 0 || watch.some(isRootWatch);
}

// placesOf the places a page rule names; none when it applies anywhere
export function placesOf(rule) {
  return isAnywhere(rule) ? [] : rule.watch;
}

// editableWatch the entries the In picker shows as chips: a lone root entry
// is "anywhere" (no chip), any other list is shown as stored
export function editableWatch(rule) {
  const watch = rule.watch || [];
  return watch.length === 1 && isRootWatch(watch[0]) ? [] : watch;
}

const tok = (s) => ({ t: "tok", s });

function orList(names) {
  const out = [];
  names.forEach((n, i) => {
    if (i > 0) out.push(i === names.length - 1 ? " or " : ", ");
    out.push(tok(n));
  });
  return out;
}

function triggerParts(t) {
  const q = (v) => `“${v || "…"}”`;
  switch (t.type) {
    case PAGE_CHANGED: return ["I ", tok("write on a page")];
    case PAGE_TAGGED: return ["I ", tok(`tag a page ${q(t.tag)}`)];
    case "finished_reading": return ["I ", tok("finish reading"), " a document"];
    case "read_progress": return ["I've ", tok(`read ${t.percent}%`), " of a document"];
    case "not_opened_for_days": return ["a document ", tok(`sits unopened ${t.days} days`)];
    case "not_modified_for_days": return ["a document ", tok(`sits unchanged ${t.days} days`)];
    case "age_since_created": return ["a document ", tok(`arrived at least ${t.days} days ago`)];
    case "tag_is": return ["I ", tok(`tag a document ${q(t.tag)}`)];
    case "name_matches": return ["a document ", tok(`named like ${q(t.pattern)}`), " is"];
    case "pinned_is": return ["a document ", tok(t.pinned ? "is pinned" : "is unpinned")];
    default: return ["a document ", tok(t.type)];
  }
}

export function destinationName(rule, ctx) {
  const cm = rule.destination && rule.destination.createIfMissing;
  if (cm) return cm.name || "…";
  return folderName(ctx, rule.destination.id, rule.destination.name);
}

// sentence the one rendering of a rule, shared by the list, the editor
// summary and the auto-name: an array of parts, each a string or
// {t: "if" | "then" | "tok", s}. Renders every trigger/action combination.
export function sentence(rule, ctx) {
  const t = rule.trigger || {};
  const parts = [{ t: "if", s: "IF" }, ...triggerParts(t)];
  if (isPageTrigger(t.type)) {
    const names = placesOf(rule).map((w) => watchName(ctx, w));
    if (names.length) parts.push(" in ", ...orList(names));
    else parts.push(" anywhere");
  } else {
    parts.push(" in ", tok(folderName(ctx, rule.source.id, rule.source.name)));
  }
  const cond = CONDITIONS.find((c) => c.scope === rule.scope);
  if (cond && !isPdfOnly(t.type)) parts.push(` (${cond.label})`);
  parts.push(" ", { t: "then", s: "THEN" });
  const key = actionKey(rule);
  if (key === "move") parts.push(tok(`move it to ${destinationName(rule, ctx)}`));
  else if (key === "notes") parts.push(tok("just save notes"));
  else parts.push(tok("extract tasks to my inbox"));
  return parts;
}

// sentenceText the sentence as plain text (shown wherever an auto-named
// rule's name would be)
export function sentenceText(rule, ctx) {
  return sentence(rule, ctx).map((p) => (typeof p === "string" ? p : p.t === "tok" ? p.s : `${p.s} `)).join("")
    .replace(/\s+/g, " ").trim();
}

// ruleLabel a rule's name for display: its own name, else its sentence
export function ruleLabel(rule, ctx) {
  return (rule && rule.name) || (rule ? sentenceText(rule, ctx) : "");
}

// normalizePageTag as the server matches page tags (NormalizeTag, the same
// as document tags): lowercased, runs of spaces, "_" and "-" become one
// space, trimmed — "To-Do", "to_do" and "to do" are one tag
export function normalizePageTag(tag) {
  return (tag || "").toLowerCase().replace(/[\s_-]+/g, " ").trim();
}

// tagSuggestions distinct page tag names, one spelling per normalised tag
export function tagSuggestions(tags) {
  const seen = new Set();
  const out = [];
  for (const t of tags || []) {
    const n = normalizePageTag(t.name);
    if (n && !seen.has(n)) {
      seen.add(n);
      out.push(n);
    }
  }
  return out;
}

// taggedCount pages carrying tag inside the rule's places and condition, from
// GET page-tags ({name, docs: [{id, folders, kind, pages}]})
export function taggedCount(tags, rule) {
  const want = normalizePageTag(rule.trigger && rule.trigger.tag);
  const hit = (tags || []).find((x) => x.name === want);
  if (!want || !hit) return 0;
  const places = placesOf(rule);
  return hit.docs.filter((d) => (rule.scope === "all" || !rule.scope || d.kind === rule.scope) &&
    (places.length === 0 || places.some((w) => (w.kind === "document" ? w.id === d.id : d.folders.includes(w.id)))))
    .reduce((n, d) => n + d.pages, 0);
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

// defaults: {prompt, model} from GET /pipeline; the UI never hard-codes them.
// Source and destination are unused by the server for pipeline rules. An
// empty watch is "anywhere" (forSave sends the root folder entry).
export function newPipelineRule(defaults) {
  return {
    id: "",
    name: "",
    enabled: true,
    source: { id: ROOT_ID, name: "" },
    scope: "all",
    trigger: { type: PAGE_CHANGED },
    destination: { id: ROOT_ID, name: "" },
    action: { type: "pipeline", prompt: (defaults && defaults.prompt) || "", model: (defaults && defaults.model) || "" },
    watch: [],
  };
}

// switchAction the rule after the trigger group changes (page ↔ document). stash holds the last
// move and pipeline versions of the draft, so switching back restores the
// edits (prompt, model, watch, source, ...). A move rule never keeps watch.
// Returns { rule, stash }; inputs are not mutated.
export function switchAction(rule, type, stash, { defaults, folders } = {}) {
  const next = { ...(stash || {}) };
  if (type === "pipeline" && !isPipeline(rule)) {
    next.move = rule;
    const p = next.pipeline || newPipelineRule(defaults);
    return { rule: { ...p, id: rule.id, name: rule.name, enabled: rule.enabled }, stash: next };
  }
  if (type === "move" && isPipeline(rule)) {
    next.pipeline = rule;
    const m = { ...(next.move || newRule(folders)) };
    delete m.watch;
    return { rule: { ...m, id: rule.id, name: rule.name, enabled: rule.enabled }, stash: next };
  }
  return { rule, stash: next };
}

// chooseTrigger the rule after an IF card is picked: a page trigger makes a
// pipeline rule, a document trigger a move rule (the other shape is kept in
// stash, so switching back restores it). A PDF-only trigger locks the
// condition to PDFs & books; leaving one drops that lock.
// Returns { rule, stash }; inputs are not mutated.
// Each trigger type's last parameters are kept in stash.triggers, so picking
// a card again restores them (minPages, settle hours, the page tag, ...).
export function chooseTrigger(rule, type, stash, opts) {
  const triggers = { ...((stash && stash.triggers) || {}), [rule.trigger.type]: rule.trigger };
  const sw = switchAction(rule, isPageTrigger(type) ? "pipeline" : "move", { ...(stash || {}), triggers }, opts);
  let r = sw.rule;
  const next = { ...sw.stash, triggers: { ...triggers } };
  if (r.trigger.type !== type) {
    next.triggers[r.trigger.type] = r.trigger;
    let scope = r.scope;
    if (isPdfOnly(type)) scope = "pdf_epub";
    else if (isPdfOnly(r.trigger.type)) scope = "all";
    r = { ...r, trigger: next.triggers[type] || makeTrigger(type), scope };
  }
  return { rule: r, stash: next };
}

// chooseAction the rule after a THEN card is picked; a card that does not
// fit the trigger leaves the rule unchanged
export function chooseAction(rule, key) {
  if (!actionFit(key, rule.trigger.type).ok || key === "move") return rule;
  const action = { ...rule.action };
  if (key === "notes") action.tasks = false;
  else delete action.tasks;
  return { ...rule, action };
}

// forSave the rule as sent to the server: a pipeline prompt/model is trimmed
// (whitespace-only → "" → the server's default); an empty watch becomes the
// root folder entry ("anywhere", both page triggers), any other watch is sent
// as it is; tasks true is the default and not stored; a move rule has no
// watch and no tasks
export function forSave(rule) {
  const r = { ...rule };
  if (isPipeline(r)) {
    const action = { ...r.action, prompt: (r.action.prompt || "").trim(), model: (r.action.model || "").trim() };
    if (action.tasks !== false) delete action.tasks;
    r.action = action;
    if (!(r.watch || []).length) r.watch = [{ id: ROOT_ID, kind: "folder", name: ROOT_NAME }];
    if (r.trigger.type === PAGE_TAGGED) r.trigger = { ...r.trigger, tag: (r.trigger.tag || "").trim() };
  } else {
    delete r.watch;
    if (r.action && "tasks" in r.action) {
      r.action = { ...r.action };
      delete r.action.tasks;
    }
  }
  return r;
}

// missingFor the watch entries of ruleId the page watch reports as gone
export function missingFor(pipeline, ruleId) {
  return ((pipeline && pipeline.missingWatches) || []).filter((m) => m.ruleId === ruleId);
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

// pendingText the header's line about planned page events not sent yet:
// dry-run holds them on purpose; live waits for them to be accepted
export function pendingText(n, live) {
  if (!n) return "";
  return live
    ? `${plural(n, "page change")} waiting to be accepted.`
    : `${plural(n, "page change")} held by Dry-run mode; switch to Live to send them.`;
}

// runCounts one page-watch run: feed events (the timeline) and rule events
// (watched pages) counted apart, so feed events never read as rule events
export function runCounts(r) {
  const toRules = r.pipelineAccepted || 0;
  const sent = Math.max(0, (r.accepted || 0) - toRules);
  let s = `${plural(sent, "page change")} to the timeline, ${toRules} to your rules`;
  if (r.mode === "dry" && r.pipeline > 0) s += ` (${r.pipeline} held by Dry-run)`;
  return s;
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
