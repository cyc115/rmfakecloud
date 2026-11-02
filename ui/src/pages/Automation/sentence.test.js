// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ACTION_CARDS, TRIGGER_CARDS, actionFit, actionKey, chooseAction, chooseTrigger, conditionLock, makeTrigger,
  editableWatch, newPipelineRule, newRule, normalizePageTag, ruleLabel, sentence, sentenceText, tagSuggestions, taggedCount,
} from "./rules.js";

const FOLDERS = [
  { id: "", name: "My files", path: "My files", depth: 0 },
  { id: "33333333-3333-4333-8333-333333333333", name: "Papers To Read", path: "Papers To Read", depth: 1 },
  { id: "inb", name: "Inbox", path: "Inbox", depth: 1 },
  { id: "arc", name: "Archive", path: "Archive", depth: 1 },
  { id: "art", name: "Articles", path: "Reading / Articles", depth: 2 },
];
const WATCHABLES = [
  { id: "44444444-4444-4444-8444-444444444444", kind: "document", name: "inbox", path: "inbox", docType: "notebook" },
  { id: "notes-doc", kind: "document", name: "Notes", path: "Projects / Notes", docType: "notebook" },
  { id: "proj", kind: "folder", name: "Projects", path: "Projects" },
];
const CTX = { folders: FOLDERS, watchables: WATCHABLES };
const DEFAULTS = { prompt: "DEFAULT", model: "m/default" };

// Mike's live rules (auto-organize.json, 2026-11-01 + the second watched notebook)
const MIKE_MOVE = {
  id: "11111111-1111-4111-8111-111111111111", name: "", enabled: true, scope: "pdf_epub",
  source: { id: "33333333-3333-4333-8333-333333333333", name: "Papers To Read" },
  trigger: { type: "finished_reading", minPages: 2, settleHours: 6, minAgeEnabled: true, minAgeDays: 0, pinned: true },
  destination: { id: "", name: "", createIfMissing: { name: "Read", parent: "source" } },
  action: { type: "move" },
};
const MIKE_PIPELINE = {
  id: "22222222-2222-4222-8222-222222222222", name: "capture journal pages", enabled: true, scope: "all",
  source: { id: "", name: "" }, destination: { id: "", name: "" },
  trigger: { type: "page_changed" },
  action: { type: "pipeline", prompt: "Mike's custom extraction prompt", model: "deepseek/deepseek-v4.1-flash" },
  watch: [
    { id: "44444444-4444-4444-8444-444444444444", kind: "document", name: "inbox" },
    { id: "notes-doc", kind: "document", name: "Notes" },
  ],
};

test("Mike's move rule renders as one sentence", () => {
  assert.equal(sentenceText(MIKE_MOVE, CTX), "IF I finish reading a document in Papers To Read THEN move it to Read");
  // without the live tree the cached names give the same sentence
  assert.equal(sentenceText(MIKE_MOVE), "IF I finish reading a document in Papers To Read THEN move it to Read");
});

test("Mike's pipeline rule renders as one sentence", () => {
  assert.equal(sentenceText(MIKE_PIPELINE, CTX), "IF I write on a page in inbox or Notes THEN extract tasks to my inbox");
  assert.equal(sentenceText(MIKE_PIPELINE), "IF I write on a page in inbox or Notes THEN extract tasks to my inbox");
});

test("sentence parts mark IF, THEN and the tokens", () => {
  const parts = sentence(MIKE_PIPELINE, CTX);
  assert.deepEqual(parts[0], { t: "if", s: "IF" });
  assert.ok(parts.some((p) => p.t === "then"));
  assert.deepEqual(parts.filter((p) => p.t === "tok").map((p) => p.s), ["write on a page", "inbox", "Notes", "extract tasks to my inbox"]);
});

function moveRule(trigger, scope = "all", extra = {}) {
  return { ...newRule(FOLDERS), source: { id: "inb", name: "Inbox" }, destination: { id: "arc", name: "Archive" }, scope, trigger, ...extra };
}

test("every document trigger with Move renders", () => {
  const cases = [
    [makeTrigger("finished_reading"), "IF I finish reading a document in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("read_progress"), percent: 80 }, "IF I've read 80% of a document in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("not_opened_for_days"), days: 30 }, "IF a document sits unopened 30 days in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("not_modified_for_days"), days: 90 }, "IF a document sits unchanged 90 days in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("age_since_created"), days: 14 }, "IF a document arrived at least 14 days ago in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("tag_is"), tag: "receipts" }, "IF I tag a document “receipts” in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("name_matches"), pattern: "WSJ*" }, "IF a document named like “WSJ*” is in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("pinned_is"), pinned: true }, "IF a document is pinned in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("pinned_is"), pinned: false }, "IF a document is unpinned in Inbox THEN move it to Archive"],
  ];
  for (const [trigger, want] of cases) assert.equal(sentenceText(moveRule(trigger), CTX), want);
  // every document card is covered
  const docTypes = TRIGGER_CARDS.filter((c) => c.group === "document").map((c) => c.type);
  assert.deepEqual([...new Set(cases.map((c) => c[0].type))].sort(), docTypes.sort());
});

test("conditions render, except the fixed PDF-only one", () => {
  const nm = { ...makeTrigger("name_matches"), pattern: "WSJ*" };
  assert.equal(sentenceText(moveRule(nm, "notebooks"), CTX), "IF a document named like “WSJ*” is in Inbox (only notebooks) THEN move it to Archive");
  assert.equal(sentenceText(moveRule(nm, "pdf_epub"), CTX), "IF a document named like “WSJ*” is in Inbox (only PDFs & books) THEN move it to Archive");
  assert.equal(sentenceText(moveRule(makeTrigger("finished_reading"), "pdf_epub"), CTX), "IF I finish reading a document in Inbox THEN move it to Archive");
});

test("move destinations: nested folder by name, root, new subfolder", () => {
  const t = makeTrigger("finished_reading");
  assert.equal(sentenceText(moveRule(t, "pdf_epub", { destination: { id: "art", name: "Articles" } }), CTX), "IF I finish reading a document in Inbox THEN move it to Articles");
  assert.equal(sentenceText(moveRule(t, "pdf_epub", { destination: { id: "", name: "" } }), CTX), "IF I finish reading a document in Inbox THEN move it to My files");
  assert.equal(sentenceText(moveRule(t, "pdf_epub", { source: { id: "", name: "" } }), CTX), "IF I finish reading a document in My files THEN move it to Archive");
});

test("page triggers × page actions × places", () => {
  const base = newPipelineRule(DEFAULTS);
  assert.equal(sentenceText(base, CTX), "IF I write on a page anywhere THEN extract tasks to my inbox");
  const rootOnly = { ...base, watch: [{ id: "", kind: "folder", name: "My files" }] };
  assert.equal(sentenceText(rootOnly, CTX), "IF I write on a page anywhere THEN extract tasks to my inbox");
  const tagged = { ...base, trigger: { type: "page_tagged", tag: "todo" }, action: { ...base.action, tasks: false } };
  assert.equal(sentenceText(tagged, CTX), "IF I tag a page “todo” anywhere THEN just save notes");
  const three = { ...tagged, action: { type: "pipeline" }, watch: [
    { id: "notes-doc", kind: "document", name: "Notes" }, { id: "proj", kind: "folder", name: "Projects" }, { id: "x", kind: "folder", name: "Old" }] };
  assert.equal(sentenceText(three, CTX), "IF I tag a page “todo” in Notes, Projects or Old THEN extract tasks to my inbox");
  const onlyNb = { ...base, scope: "notebooks", watch: [{ id: "proj", kind: "folder" }] };
  assert.equal(sentenceText(onlyNb, CTX), "IF I write on a page in Projects (only notebooks) THEN extract tasks to my inbox");
  const noTag = { ...base, trigger: { type: "page_tagged", tag: "" } };
  assert.equal(sentenceText(noTag, CTX), "IF I tag a page “…” anywhere THEN extract tasks to my inbox");
});

test("action cards fit only their trigger group, with a reason", () => {
  for (const card of ACTION_CARDS) {
    for (const t of TRIGGER_CARDS) {
      const fit = actionFit(card.key, t.type);
      assert.equal(fit.ok, card.for === t.group, `${card.key} on ${t.type}`);
      assert.equal(fit.reason === "", fit.ok);
    }
  }
  assert.match(actionFit("move", "page_tagged").reason, /whole documents/);
});

test("conditionLock explains the PDF-only triggers", () => {
  assert.match(conditionLock("finished_reading"), /^Fixed: “I finish reading” .* only PDFs & books/);
  assert.match(conditionLock("read_progress"), /PDFs & books/);
  assert.equal(conditionLock("name_matches"), "");
  assert.equal(conditionLock("page_changed"), "");
});

test("chooseTrigger switches shape, keeps the other shape, locks and unlocks the PDF condition", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let { rule, stash } = chooseTrigger(newPipelineRule(DEFAULTS), "page_tagged", null, opts);
  assert.equal(rule.action.type, "pipeline");
  assert.deepEqual(rule.trigger, { type: "page_tagged", tag: "" });
  rule = { ...rule, trigger: { ...rule.trigger, tag: "todo" }, watch: [{ id: "proj", kind: "folder" }] };
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.equal(rule.action.type, "move");
  assert.equal(rule.scope, "pdf_epub");
  assert.equal("watch" in rule, false);
  ({ rule, stash } = chooseTrigger(rule, "name_matches", stash, opts));
  assert.equal(rule.scope, "all", "leaving a PDF-only trigger drops the lock");
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(rule.trigger.tag, "todo", "the page draft comes back");
  assert.deepEqual(rule.watch, [{ id: "proj", kind: "folder" }]);
});

test("chooseAction toggles tasks on page rules and ignores cards that do not fit", () => {
  const p = newPipelineRule(DEFAULTS);
  const notes = chooseAction(p, "notes");
  assert.equal(notes.action.tasks, false);
  assert.equal(actionKey(notes), "notes");
  assert.equal(actionKey(chooseAction(notes, "extract")), "extract");
  assert.equal(chooseAction(p, "move"), p);
  const m = newRule(FOLDERS);
  assert.equal(chooseAction(m, "notes"), m);
  assert.equal(actionKey(m), "move");
});

test("taggedCount counts pages in the rule's places and condition", () => {
  const tags = [{ name: "todo", pages: 4, docs: [
    { id: "notes-doc", folders: ["proj", ""], kind: "notebooks", pages: 2 },
    { id: "paper", folders: ["inb", ""], kind: "pdf_epub", pages: 1 },
    { id: "deep", folders: ["alpha", "proj", ""], kind: "notebooks", pages: 1 },
  ] }];
  const r = { ...newPipelineRule(DEFAULTS), trigger: { type: "page_tagged", tag: " TODO " } };
  assert.equal(taggedCount(tags, r), 4);
  assert.equal(taggedCount(tags, { ...r, watch: [{ id: "proj", kind: "folder" }] }), 3);
  assert.equal(taggedCount(tags, { ...r, watch: [{ id: "paper", kind: "document" }] }), 1);
  assert.equal(taggedCount(tags, { ...r, scope: "notebooks" }), 3);
  assert.equal(taggedCount(tags, { ...r, trigger: { type: "page_tagged", tag: "other" } }), 0);
  assert.equal(taggedCount([], r), 0);
});

test("page tags normalise like document tags; suggestions keep one spelling", () => {
  for (const v of ["To-Do", "to_do", "  to   do ", "TO--DO"]) assert.equal(normalizePageTag(v), "to do");
  assert.deepEqual(tagSuggestions([{ name: "to do" }, { name: "To-Do" }, { name: "asset mgmt" }, { name: "" }]), ["to do", "asset mgmt"]);
  const tags = [{ name: "to do", docs: [{ id: "d", folders: [""], kind: "notebooks", pages: 2 }] }];
  assert.equal(taggedCount(tags, { ...newPipelineRule(DEFAULTS), trigger: { type: "page_tagged", tag: "To_Do" } }), 2);
});

test("a root entry among other places renders as anywhere and stays editable as stored", () => {
  const r = { ...newPipelineRule(DEFAULTS), watch: [{ id: "", kind: "folder", name: "My files" }, { id: "notes-doc", kind: "document", name: "Notes" }] };
  assert.equal(sentenceText(r, CTX), "IF I write on a page anywhere THEN extract tasks to my inbox");
  assert.deepEqual(editableWatch(r), r.watch);
  assert.deepEqual(editableWatch({ ...r, watch: [{ id: "", kind: "folder" }] }), []);
  const tags = [{ name: "todo", docs: [{ id: "x", folders: ["other", ""], kind: "notebooks", pages: 3 }] }];
  assert.equal(taggedCount(tags, { ...r, trigger: { type: "page_tagged", tag: "todo" } }), 3);
});

test("switching triggers and back keeps each trigger's parameters", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let rule = { ...MIKE_MOVE };
  let stash = null;
  ({ rule, stash } = chooseTrigger(rule, "read_progress", stash, opts));
  assert.equal(rule.trigger.percent, 80);
  rule = { ...rule, trigger: { ...rule.trigger, percent: 55 } };
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.deepEqual(rule.trigger, MIKE_MOVE.trigger, "minPages 2, settle 6, min age 0 kept");
  ({ rule, stash } = chooseTrigger(rule, "read_progress", stash, opts));
  assert.equal(rule.trigger.percent, 55);
  // the page tag survives page_changed and a trip through a document trigger
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  rule = { ...rule, trigger: { ...rule.trigger, tag: "todo" } };
  ({ rule, stash } = chooseTrigger(rule, "page_changed", stash, opts));
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(rule.trigger.tag, "todo");
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.equal(rule.trigger.minPages, 2);
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(rule.trigger.tag, "todo");
});

test("ruleLabel: its own name, else its sentence", () => {
  assert.equal(ruleLabel(MIKE_PIPELINE, CTX), "capture journal pages");
  assert.equal(ruleLabel(MIKE_MOVE, CTX), "IF I finish reading a document in Papers To Read THEN move it to Read");
});
