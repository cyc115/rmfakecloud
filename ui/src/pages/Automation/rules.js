// Rule vocabulary for the Automation page. The server is the authority on
// validation; these helpers build the rule sentences, the editor's card
// catalogue and sensible defaults. The stored format is automation.json v2:
// {id, name, enabled, when: {subject, in, trigger}, conditions, actions}.
// A document rule has one folder in `in` and one move, add_tag or remove_tag
// action; a page rule has places in `in` and one extract_tasks or save_notes
// action (R-B2).

import { TIME_DEFAULT, conditionLock, conditionPhrase, isPdfOnly, makeCondition, timePhrase } from "./conditions.js";

export { conditionLock, isPdfOnly };

export const ROOT_ID = "";
export const ROOT_NAME = "My files";
export const NO_AUTOMATION_TAG = "no automation";

export const PAGE_CHANGED = "page_changed";
export const PAGE_TAGGED = "page_tagged";

// The IF cards. group is the rule's subject: "page" triggers make page rules,
// "document" triggers make document rules. pdfOnly triggers read the page you
// stopped on, which only PDFs and books report, so their kind_is is fixed.
export const TRIGGER_CARDS = [
  { type: PAGE_CHANGED, group: "page", icon: "✍️", title: "I write on a page", sub: "new or changed handwriting" },
  { type: PAGE_TAGGED, group: "page", icon: "🏷️", title: "I tag a page", sub: "with a page tag you choose" },
  { type: "finished_reading", group: "document", icon: "📖", title: "I finish reading", sub: "read to the last page", pdfOnly: true },
  { type: "read_progress", group: "document", icon: "📊", title: "I've read X%", sub: "part of the way through", pdfOnly: true },
  { type: "not_opened_for_days", group: "document", icon: "💤", title: "It sits unopened", sub: "for N days" },
  { type: "not_modified_for_days", group: "document", icon: "🕰️", title: "It sits unchanged", sub: "no edits for N days" },
  { type: "age_since_created", group: "document", icon: "📥", title: "It arrives", sub: "N days after it lands" },
  { type: "created_time", group: "document", icon: "🆕", title: "It was created…", sub: "in the last / more than N hours, days, weeks ago" },
  { type: "modified_time", group: "document", icon: "✏️", title: "It was changed…", sub: "in the last / more than N hours, days, weeks ago" },
  { type: "tag_is", group: "document", icon: "🔖", title: "I tag a document", sub: "with a tag you choose" },
  { type: "name_matches", group: "document", icon: "🔤", title: "Its name matches", sub: "a pattern like WSJ*" },
  { type: "pinned_is", group: "document", icon: "📌", title: "It's pinned / unpinned", sub: "pinned or not" },
];

export const PDF_ONLY_BADGE = "PDFs & books";

// The THEN cards: one per rule (R-B2). "for" is the subject the action works
// on; "type" is the stored action type.
export const ACTION_CARDS = [
  { key: "extract", type: "extract_tasks", for: "page", icon: "✅", title: "Extract tasks to my inbox", sub: "+ save the page to its note" },
  { key: "notes", type: "save_notes", for: "page", icon: "📝", title: "Just save notes", sub: "transcribe, no tasks" },
  { key: "move", type: "move", for: "document", icon: "📂", title: "Move it to…", sub: "works on documents" },
  { key: "addtag", type: "add_tag", for: "document", icon: "🏷️", title: "Add a tag", sub: "a document tag you choose" },
  { key: "rmtag", type: "remove_tag", for: "document", icon: "🧽", title: "Remove a tag", sub: "a document tag you choose" },
];

// isTagAction add_tag or remove_tag
export function isTagAction(type) {
  return type === "add_tag" || type === "remove_tag";
}

// the trigger parameters, as the catalogue names them (settleHours only for
// the reading triggers, P2; min age is a min_age_days condition, P1)
export const TRIGGER_DEFAULTS = {
  finished_reading: { minPages: 3, settleHours: 6 },
  read_progress: { percent: 80, settleHours: 6 },
  not_opened_for_days: { days: 30 },
  not_modified_for_days: { days: 90 },
  name_matches: { pattern: "" },
  tag_is: { tag: "" },
  age_since_created: { days: 14 },
  pinned_is: { pinned: true },
  page_tagged: { tag: "" },
  created_time: TIME_DEFAULT,
  modified_time: TIME_DEFAULT,
};

export const INTERVALS = [5, 10, 15, 30, 60];

export function triggerCard(type) {
  return TRIGGER_CARDS.find((c) => c.type === type);
}

export function isPageTrigger(type) {
  return type === PAGE_CHANGED || type === PAGE_TAGGED;
}

export function makeTrigger(type) {
  return { type, ...(TRIGGER_DEFAULTS[type] || {}) };
}

export function isPageRule(rule) {
  return !!rule && !!rule.when && rule.when.subject === "page";
}

// the rule's one action (R-B2); a rule without one reads as its subject's default
export function actionOf(rule) {
  const a = (rule && rule.actions) || [];
  if (a.length) return a[0];
  return isPageRule(rule) ? { type: "extract_tasks" } : { type: "move", to: { id: ROOT_ID, name: "" } };
}

// sourceOf a document rule's one folder (in[0])
export function sourceOf(rule) {
  return ((rule && rule.when && rule.when.in) || [])[0] || { id: ROOT_ID, kind: "folder", name: "" };
}

// moveTo a document rule's destination
export function moveTo(rule) {
  return actionOf(rule).to || { id: ROOT_ID, name: "" };
}

function conditionsOf(rule) {
  return (rule && rule.conditions) || [];
}

// kindOf the rule's kind_is kind, "" when it has none (all)
export function kindOf(rule) {
  const c = conditionsOf(rule).find((x) => x.type === "kind_is");
  return c && c.kind !== "all" ? c.kind || "" : "";
}

// actionKey the THEN card of a rule
export function actionKey(rule) {
  const t = actionOf(rule).type;
  const card = ACTION_CARDS.find((a) => a.type === t);
  return card ? card.key : "move";
}

// actionFit whether an action card fits the trigger, and why not
export function actionFit(key, triggerType) {
  const page = isPageTrigger(triggerType);
  const card = ACTION_CARDS.find((a) => a.key === key);
  if (card.for === "document" && page) {
    return { ok: false, reason: "Moving and tagging work on whole documents, so they can't follow a page trigger. Pick a trigger under “On a document” to move or tag things." };
  }
  if (card.for === "page" && !page) {
    return { ok: false, reason: "Works on pages: pick “I write on a page” or “I tag a page” to extract tasks or save notes." };
  }
  return { ok: true, reason: "" };
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

function placeName(ctx, w) {
  if (w.kind === "folder" && w.id === ROOT_ID) return ROOT_NAME;
  const hit = ((ctx && ctx.watchables) || []).find((i) => i.id === w.id && i.kind === w.kind);
  return hit ? hit.name : w.name || w.id;
}

export function isRootPlace(w) {
  return w.kind === "folder" && w.id === ROOT_ID;
}

function inOf(rule) {
  return (rule.when && rule.when.in) || [];
}

// isAnywhere the rule's page places cover the whole tree: no entry, or the
// root folder among them (a root entry next to others still means anywhere)
export function isAnywhere(rule) {
  const places = inOf(rule);
  return places.length === 0 || places.some(isRootPlace);
}

// placesOf the places a page rule names; none when it applies anywhere
export function placesOf(rule) {
  return isAnywhere(rule) ? [] : inOf(rule);
}

// editableIn the entries the In picker shows as chips: a lone root entry
// is "anywhere" (no chip), any other list is shown as stored
export function editableIn(rule) {
  const places = inOf(rule);
  return places.length === 1 && isRootPlace(places[0]) ? [] : places;
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
    case "created_time": return ["a document ", tok(`was ${timePhrase("created", t)}`)];
    case "modified_time": return ["a document ", tok(`was ${timePhrase("changed", t)}`)];
    case "tag_is": return ["I ", tok(`tag a document ${q(t.tag)}`)];
    case "name_matches": return ["a document ", tok(`named like ${q(t.pattern)}`), " is"];
    case "pinned_is": return ["a document ", tok(t.pinned ? "is pinned" : "is unpinned")];
    default: return ["a document ", tok(t.type)];
  }
}

// tagActionText add tag “new” / remove tag “new”
export function tagActionText(a) {
  return `${a.type === "remove_tag" ? "remove" : "add"} tag “${(a.tag || "").trim() || "…"}”`;
}

// changeText what one planned or logged change does: "→ Read" for a move,
// "add tag “new”" for a tag change
export function changeText(m) {
  return isTagAction(m.action) ? tagActionText(m) : `→ ${m.toName}`;
}

// changeVerb "moved" / "tagged" / "untagged", or "would have …" for dry runs
export function changeVerb(m) {
  const verb = m.action === "add_tag" ? "tagged" : m.action === "remove_tag" ? "untagged" : "moved";
  return m.mode === "LIVE" ? verb : `would have ${verb}`;
}

export function destinationName(rule, ctx) {
  const to = moveTo(rule);
  if (to.createIfMissing) return to.createIfMissing.name || "…";
  return folderName(ctx, to.id, to.name);
}

// sentence the one rendering of a rule, shared by the list, the editor
// summary and the auto-name: an array of parts, each a string or
// {t: "if" | "then" | "tok", s}. The kind_is part follows the places (not for
// a PDF-only trigger, whose kind is fixed); every other condition follows as
// " AND " + its phrase, in stored order.
export function sentence(rule, ctx) {
  const t = (rule.when && rule.when.trigger) || {};
  const parts = [{ t: "if", s: "IF" }, ...triggerParts(t)];
  if (isPageRule(rule)) {
    const names = placesOf(rule).map((w) => placeName(ctx, w));
    if (names.length) parts.push(" in ", ...orList(names));
    else parts.push(" anywhere");
  } else {
    const src = sourceOf(rule);
    // a tag rule covers its folder and every folder inside it
    if (isTagAction(actionOf(rule).type)) {
      if (src.id === ROOT_ID) parts.push(" anywhere");
      else parts.push(" in ", tok(folderName(ctx, src.id, src.name)), " or its subfolders");
    } else parts.push(" in ", tok(folderName(ctx, src.id, src.name)));
  }
  const kind = conditionsOf(rule).find((c) => c.type === "kind_is");
  const kindText = kind ? conditionPhrase(kind) : "";
  if (kindText && !isPdfOnly(t.type)) parts.push(` (${kindText})`);
  for (const c of conditionsOf(rule)) {
    if (c.type !== "kind_is") parts.push(" AND ", tok(conditionPhrase(c)));
  }
  parts.push(" ", { t: "then", s: "THEN" });
  const key = actionKey(rule);
  if (key === "move") parts.push(tok(`move it to ${destinationName(rule, ctx)}`));
  else if (key === "addtag" || key === "rmtag") parts.push(tok(tagActionText(actionOf(rule))));
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

// pageTagsOf the page tags a page must carry, as the server's Rule.PageTags
// (P4): the page_tagged trigger's tag plus every page_tag_is condition,
// normalised, deduped and sorted
export function pageTagsOf(rule) {
  const set = new Set();
  const t = (rule.when && rule.when.trigger) || {};
  if (t.type === PAGE_TAGGED && normalizePageTag(t.tag)) set.add(normalizePageTag(t.tag));
  for (const c of conditionsOf(rule)) {
    if (c.type === "page_tag_is" && normalizePageTag(c.tag)) set.add(normalizePageTag(c.tag));
  }
  return [...set].sort();
}

// the conditions taggedCount can test from GET page-tags
const COUNTABLE = new Set(["kind_is", "page_tag_is"]);

// taggedCount pages carrying the rule's one page tag, inside its places and
// kind, from GET page-tags ({name, docs: [{id, folders, kind, pages}]}).
// null when the list cannot say: no tag set, two or more tags (it has
// per-document counts only, so the overlap is unknown), or a condition it
// cannot test (tag_is, tag_is_not, name_matches).
export function taggedCount(tags, rule) {
  const want = pageTagsOf(rule);
  if (want.length !== 1 || conditionsOf(rule).some((c) => !COUNTABLE.has(c.type))) return null;
  const hit = (tags || []).find((x) => x.name === want[0]);
  if (!hit) return 0;
  const kind = kindOf(rule);
  const places = placesOf(rule);
  return hit.docs.filter((d) => (!kind || d.kind === kind) &&
    (places.length === 0 || places.some((w) => (w.kind === "document" ? w.id === d.id : d.folders.includes(w.id)))))
    .reduce((n, d) => n + d.pages, 0);
}

// taggedNow the editor's "N pages are tagged … now" count: taggedCount from
// the page-tag list (null while it is not loaded), else the PagePreview's
// pages (preview: {pages} or null); null when the rule has no page tag or
// neither source can say
export function taggedNow(tags, rule, preview) {
  if (!pageTagsOf(rule).length) return null;
  const n = tags ? taggedCount(tags, rule) : null;
  if (n !== null) return n;
  return preview && typeof preview.pages === "number" ? preview.pages : null;
}

export function newRule(folders) {
  const first = (folders || []).find((f) => f.id !== ROOT_ID);
  return {
    id: "",
    name: "",
    enabled: true,
    when: {
      subject: "document",
      in: [{ id: first ? first.id : ROOT_ID, kind: "folder", name: first ? first.name : ROOT_NAME }],
      trigger: makeTrigger("finished_reading"),
    },
    conditions: [],
    actions: [{ type: "move", to: { id: ROOT_ID, name: "" } }],
  };
}

// defaults: {prompt, model} from GET /pipeline; the UI never hard-codes them.
// An empty `in` is "anywhere" (forSave sends the root folder entry).
export function newPageRule(defaults) {
  return {
    id: "",
    name: "",
    enabled: true,
    when: { subject: "page", in: [], trigger: { type: PAGE_CHANGED } },
    conditions: [],
    actions: [{ type: "extract_tasks", prompt: (defaults && defaults.prompt) || "", model: (defaults && defaults.model) || "" }],
  };
}

const withoutKind = (conds) => conds.filter((c) => c.type !== "kind_is");
const NOT_OPENED = "not_opened_for_days";

// chooseTrigger the rule after an IF card is picked. A page trigger makes a
// page rule, a document trigger a document rule; the other subject's draft
// is kept in stash (stash.page, stash.document), so switching back restores
// its places, conditions and action (prompt, model, destination, ...). Each
// trigger type's last parameters are kept in stash.triggers. A PDF-only
// trigger implies kind_is pdf_epub (forSave adds it), so entering or leaving
// one drops any kind_is. The first pick of "It sits unopened" adds "at least
// 7 days old" (Option A's default min age: upload-only documents have never
// been opened and would match at once). That chip is the user's to keep or
// remove: leaving "It sits unopened" drops it only while it is untouched (the
// same object, days 7; stash.minAge tracks it), since a reading rule has no
// min age (P1). Picking "It sits unopened" again then adds it again; a chip
// the user removed stays removed.
// Returns { rule, stash }; inputs are not mutated.
export function chooseTrigger(rule, type, stash, { defaults, folders } = {}) {
  const cur = rule.when.trigger;
  const next = { ...(stash || {}), triggers: { ...((stash && stash.triggers) || {}), [cur.type]: cur } };
  const subject = isPageTrigger(type) ? "page" : "document";
  let r = rule;
  if (rule.when.subject !== subject) {
    next[rule.when.subject] = rule;
    const base = next[subject] || (subject === "page" ? newPageRule(defaults) : newRule(folders));
    r = { ...base, id: rule.id, name: rule.name, enabled: rule.enabled };
  }
  if (r.when.trigger.type !== type) {
    const from = r.when.trigger;
    if (!next.triggers[from.type]) next.triggers[from.type] = from;
    let conditions = conditionsOf(r);
    if (isPdfOnly(type) || isPdfOnly(from.type)) conditions = withoutKind(conditions);
    if (from.type === NOT_OPENED && next.minAge && typeof next.minAge === "object") {
      const auto = next.minAge;
      if (!conditions.includes(auto)) next.minAge = "removed";
      else if (auto.days === 7) {
        conditions = conditions.filter((c) => c !== auto);
        next.minAge = "dropped";
      }
    }
    if (type === NOT_OPENED && (!next.triggers[type] || next.minAge === "dropped") &&
      !conditions.some((c) => c.type === "min_age_days")) {
      next.minAge = makeCondition("min_age_days");
      conditions = [...conditions, next.minAge];
    }
    r = { ...r, when: { ...r.when, trigger: next.triggers[type] || makeTrigger(type) }, conditions };
  } else if (isPdfOnly(type)) {
    r = { ...r, conditions: withoutKind(conditionsOf(r)) };
  }
  return { rule: r, stash: next };
}

// chooseAction the rule after a THEN card is picked; a card that does not
// fit the trigger leaves the rule unchanged. A page rule keeps its prompt and
// model when switching between extract and notes; a document rule keeps its
// destination and tag when switching between move and the tag actions.
export function chooseAction(rule, key) {
  if (!actionFit(key, rule.when.trigger.type).ok) return rule;
  const card = ACTION_CARDS.find((a) => a.key === key);
  const cur = actionOf(rule);
  if (cur.type === card.type) return rule;
  const next = { ...cur, type: card.type };
  if (card.type === "move" && !next.to) next.to = { id: ROOT_ID, name: "" };
  if (isTagAction(card.type) && next.tag === undefined) next.tag = "";
  return { ...rule, actions: [next] };
}

// forSave the rule as sent to the server:
//   - conditions: kind_is all/blank is dropped; a PDF-only trigger gets
//     kind_is pdf_epub first (once); page tags are trimmed
//   - page rule: an empty `in` becomes the root folder entry ("anywhere",
//     both page triggers), any other `in` is sent as it is; the page action's
//     prompt/model are trimmed (whitespace-only → "" → the server's default)
//   - exactly one action (R-B2): move carries only `to`, a tag action only
//     its trimmed `tag`
export function forSave(rule) {
  const r = JSON.parse(JSON.stringify(rule));
  const t = r.when.trigger;
  let conds = conditionsOf(r).filter((c) => !(c.type === "kind_is" && (!c.kind || c.kind === "all")));
  if (isPdfOnly(t.type)) conds = [{ type: "kind_is", kind: "pdf_epub" }, ...withoutKind(conds)];
  r.conditions = conds.map((c) => (c.type === "page_tag_is" ? { ...c, tag: (c.tag || "").trim() } : c));
  const a = actionOf(rule);
  if (isPageRule(r)) {
    if (!inOf(r).length) r.when.in = [{ id: ROOT_ID, kind: "folder", name: ROOT_NAME }];
    if (t.type === PAGE_TAGGED) r.when.trigger = { ...t, tag: (t.tag || "").trim() };
    r.actions = [{ type: a.type, prompt: (a.prompt || "").trim(), model: (a.model || "").trim() }];
  } else if (isTagAction(a.type)) {
    r.actions = [{ type: a.type, tag: (a.tag || "").trim() }];
  } else {
    r.actions = [{ type: "move", to: JSON.parse(JSON.stringify(a.to || { id: ROOT_ID, name: "" })) }];
  }
  return r;
}

// missingFor the places of ruleId the page watch reports as gone
export function missingFor(pipeline, ruleId) {
  return ((pipeline && pipeline.missingWatches) || []).filter((m) => m.ruleId === ruleId);
}

export function plural(n, word) {
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
  if (rr) rule.when.in = [{ id: rr.id, kind: "folder", name: rr.name }];
  rule.actions = [{ type: "move", to: { id: "", name: "", createIfMissing: { name: "Read", parent: "source" } } }];
  return rule;
}

// withDisplayNames caches display names next to the UUIDs of a document rule
// (the UUID stays the only key); page rules keep their names as they are
export function withDisplayNames(rule, folders) {
  const r = JSON.parse(JSON.stringify(rule));
  const name = (id) => {
    const f = (folders || []).find((x) => x.id === id);
    if (!f) return null;
    return id === ROOT_ID ? ROOT_NAME : lastSegment(f.path);
  };
  const src = r.when.in[0];
  if (src && name(src.id) !== null) src.name = name(src.id);
  const a = (r.actions || [])[0];
  if (a && a.to && !a.to.createIfMissing && name(a.to.id) !== null) a.to.name = name(a.to.id);
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
