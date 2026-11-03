// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ACTION_CARDS, TRIGGER_CARDS, actionFit, actionKey, chooseAction, chooseTrigger, conditionLock, makeTrigger, triggerCard,
  editableIn, newPageRule, newRule, normalizePageTag, ruleLabel, sentence, sentenceText, tagSuggestions, taggedCount,
} from "./rules.js";

// synthetic fixtures only (R-B1)
const FOLDERS = [
  { id: "", name: "My files", path: "My files", depth: 0 },
  { id: "f-reading", name: "Reading List", path: "Reading List", depth: 1 },
  { id: "f-done", name: "Done", path: "Reading List / Done", depth: 2 },
  { id: "inb", name: "Inbox", path: "Inbox", depth: 1 },
  { id: "arc", name: "Archive", path: "Archive", depth: 1 },
  { id: "old", name: "Old", path: "Archive / Old", depth: 2 },
  { id: "art", name: "Articles", path: "Reading / Articles", depth: 2 },
];
const WATCHABLES = [
  { id: "d-journal", kind: "document", name: "Journal", path: "Journal", docType: "notebook" },
  { id: "d-ideas", kind: "document", name: "Ideas", path: "Projects / Ideas", docType: "notebook" },
  { id: "proj", kind: "folder", name: "Projects", path: "Projects" },
];
const CTX = { folders: FOLDERS, watchables: WATCHABLES };
const DEFAULTS = { prompt: "DEFAULT", model: "m/default" };

const GOLDEN_MOVE = {
  id: "0a1b2c3d-0000-4000-8000-000000000001", name: "", enabled: true,
  when: {
    subject: "document",
    in: [{ id: "f-reading", kind: "folder", name: "Reading List" }],
    trigger: { type: "finished_reading", minPages: 2, settleHours: 6 },
  },
  conditions: [{ type: "kind_is", kind: "pdf_epub" }],
  actions: [{ type: "move", to: { id: "f-done", name: "Done" } }],
};
const GOLDEN_PAGE = {
  id: "0a1b2c3d-0000-4000-8000-000000000002", name: "journal pages", enabled: true,
  when: {
    subject: "page",
    in: [{ id: "d-journal", kind: "document", name: "Journal" }, { id: "d-ideas", kind: "document", name: "Ideas" }],
    trigger: { type: "page_changed" },
  },
  conditions: [],
  actions: [{ type: "extract_tasks", prompt: "A synthetic prompt", model: "m/synthetic" }],
};

test("golden move rule renders", () => {
  assert.equal(sentenceText(GOLDEN_MOVE, CTX), "IF I finish reading a document in Reading List THEN move it to Done");
  // without the live tree the cached names give the same sentence
  assert.equal(sentenceText(GOLDEN_MOVE), "IF I finish reading a document in Reading List THEN move it to Done");
});

test("golden page rule renders", () => {
  assert.equal(sentenceText(GOLDEN_PAGE, CTX), "IF I write on a page in Journal or Ideas THEN extract tasks & annotations");
  assert.equal(sentenceText(GOLDEN_PAGE), "IF I write on a page in Journal or Ideas THEN extract tasks & annotations");
});

test("multi-condition document sentence", () => {
  const r = {
    ...GOLDEN_MOVE,
    when: { subject: "document", in: [{ id: "arc", kind: "folder", name: "Archive" }], trigger: { type: "not_opened_for_days", days: 30 } },
    conditions: [{ type: "kind_is", kind: "pdf_epub" }, { type: "min_age_days", days: 7 }, { type: "tag_is", tag: "keep" }],
    actions: [{ type: "move", to: { id: "old", name: "Old" } }],
  };
  assert.equal(sentenceText(r, CTX), "IF a document sits unopened 30 days in Archive (only PDFs & books) AND at least 7 days old AND tagged “keep” THEN move it to Old");
  // the kind_is part follows the places wherever it is stored
  const reordered = { ...r, conditions: [r.conditions[1], r.conditions[2], r.conditions[0]] };
  assert.equal(sentenceText(reordered, CTX), sentenceText(r, CTX));
});

test("multi-condition page sentence", () => {
  const r = {
    ...newPageRule(DEFAULTS),
    conditions: [{ type: "kind_is", kind: "notebooks" }, { type: "name_matches", pattern: "Journal*" }],
    actions: [{ type: "save_notes", prompt: "", model: "" }],
  };
  assert.equal(sentenceText(r, CTX), "IF I write on a page anywhere (only notebooks) AND named like “Journal*” THEN save notes & annotations");
});

test("sentence parts mark IF, THEN and the tokens", () => {
  const parts = sentence(GOLDEN_PAGE, CTX);
  assert.deepEqual(parts[0], { t: "if", s: "IF" });
  assert.ok(parts.some((p) => p.t === "then"));
  assert.deepEqual(parts.filter((p) => p.t === "tok").map((p) => p.s), ["write on a page", "Journal", "Ideas", "extract tasks & annotations"]);
});

function moveRule(trigger, kind = "", extra = {}) {
  return {
    ...newRule(FOLDERS),
    when: { subject: "document", in: [{ id: "inb", kind: "folder", name: "Inbox" }], trigger },
    conditions: kind ? [{ type: "kind_is", kind }] : [],
    actions: [{ type: "move", to: { id: "arc", name: "Archive" } }],
    ...extra,
  };
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
    [makeTrigger("created_time"), "IF a document was created in the last 24 hours in Inbox THEN move it to Archive"],
    [{ ...makeTrigger("modified_time"), compare: "older", amount: 3, unit: "days" },
      "IF a document was changed more than 3 days ago in Inbox THEN move it to Archive"],
  ];
  for (const [trigger, want] of cases) assert.equal(sentenceText(moveRule(trigger), CTX), want);
  // every document card is covered
  const docTypes = TRIGGER_CARDS.filter((c) => c.group === "document").map((c) => c.type);
  assert.deepEqual([...new Set(cases.map((c) => c[0].type))].sort(), docTypes.sort());
});

test("kind_is renders, except the fixed PDF-only one", () => {
  const nm = { ...makeTrigger("name_matches"), pattern: "WSJ*" };
  assert.equal(sentenceText(moveRule(nm, "notebooks"), CTX), "IF a document named like “WSJ*” is in Inbox (only notebooks) THEN move it to Archive");
  assert.equal(sentenceText(moveRule(nm, "pdf_epub"), CTX), "IF a document named like “WSJ*” is in Inbox (only PDFs & books) THEN move it to Archive");
  assert.equal(sentenceText(moveRule(nm, "all"), CTX), "IF a document named like “WSJ*” is in Inbox THEN move it to Archive");
  assert.equal(sentenceText(moveRule(makeTrigger("finished_reading"), "pdf_epub"), CTX), "IF I finish reading a document in Inbox THEN move it to Archive");
});

test("move destinations: nested folder by name, root, new subfolder", () => {
  const t = makeTrigger("finished_reading");
  const to = (dest) => ({ actions: [{ type: "move", to: dest }] });
  assert.equal(sentenceText(moveRule(t, "pdf_epub", to({ id: "art", name: "Articles" })), CTX), "IF I finish reading a document in Inbox THEN move it to Articles");
  assert.equal(sentenceText(moveRule(t, "pdf_epub", to({ id: "", name: "" })), CTX), "IF I finish reading a document in Inbox THEN move it to My files");
  assert.equal(sentenceText(moveRule(t, "pdf_epub", to({ id: "", name: "", createIfMissing: { name: "Read", parent: "source" } })), CTX),
    "IF I finish reading a document in Inbox THEN move it to Read");
  const atRoot = moveRule(t, "pdf_epub", { when: { subject: "document", in: [{ id: "", kind: "folder", name: "" }], trigger: t } });
  assert.equal(sentenceText(atRoot, CTX), "IF I finish reading a document in My files THEN move it to Archive");
});

test("page triggers × page actions × places", () => {
  const base = newPageRule(DEFAULTS);
  const withWhen = (r, patch) => ({ ...r, when: { ...r.when, ...patch } });
  assert.equal(sentenceText(base, CTX), "IF I write on a page anywhere THEN extract tasks & annotations");
  assert.equal(sentenceText(withWhen(base, { in: [{ id: "", kind: "folder", name: "My files" }] }), CTX), "IF I write on a page anywhere THEN extract tasks & annotations");
  const tagged = { ...withWhen(base, { trigger: { type: "page_tagged", tag: "todo" } }), actions: [{ type: "save_notes" }] };
  assert.equal(sentenceText(tagged, CTX), "IF I tag a page “todo” anywhere THEN save notes & annotations");
  const three = { ...withWhen(tagged, { in: [
    { id: "d-ideas", kind: "document", name: "Ideas" }, { id: "proj", kind: "folder", name: "Projects" }, { id: "x", kind: "folder", name: "Gone" }] }),
  actions: [{ type: "extract_tasks" }] };
  assert.equal(sentenceText(three, CTX), "IF I tag a page “todo” in Ideas, Projects or Gone THEN extract tasks & annotations");
  const onlyNb = { ...withWhen(base, { in: [{ id: "proj", kind: "folder" }] }), conditions: [{ type: "kind_is", kind: "notebooks" }] };
  assert.equal(sentenceText(onlyNb, CTX), "IF I write on a page in Projects (only notebooks) THEN extract tasks & annotations");
  const noTag = withWhen(base, { trigger: { type: "page_tagged", tag: "" } });
  assert.equal(sentenceText(noTag, CTX), "IF I tag a page “…” anywhere THEN extract tasks & annotations");
  const twoTags = { ...tagged, conditions: [{ type: "page_tag_is", tag: "urgent" }] };
  assert.equal(sentenceText(twoTags, CTX), "IF I tag a page “todo” anywhere AND on pages tagged “urgent” THEN save notes & annotations");
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

test("conditionLock explains the PDF-only triggers with the card titles", () => {
  assert.match(conditionLock("finished_reading"), /^Fixed: “I finish reading” .* only PDFs & books/);
  assert.match(conditionLock("read_progress"), /PDFs & books/);
  for (const c of TRIGGER_CARDS.filter((x) => x.pdfOnly)) assert.ok(conditionLock(c.type).includes(`“${triggerCard(c.type).title}”`), c.type);
  assert.equal(conditionLock("name_matches"), "");
  assert.equal(conditionLock("page_changed"), "");
});

test("chooseTrigger switches subject, keeps the other draft, locks and unlocks the PDF condition", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let { rule, stash } = chooseTrigger(newPageRule(DEFAULTS), "page_tagged", null, opts);
  assert.equal(rule.when.subject, "page");
  assert.deepEqual(rule.when.trigger, { type: "page_tagged", tag: "" });
  rule = { ...rule, when: { ...rule.when, trigger: { ...rule.when.trigger, tag: "todo" }, in: [{ id: "proj", kind: "folder" }] } };
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.equal(rule.when.subject, "document");
  assert.equal(actionKey(rule), "move");
  assert.equal(rule.conditions.some((c) => c.type === "kind_is"), false, "the PDF lock is implied, not a chip");
  assert.equal(rule.when.in.length, 1);
  ({ rule, stash } = chooseTrigger({ ...rule, conditions: [{ type: "tag_is", tag: "keep" }] }, "name_matches", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "tag_is", tag: "keep" }], "leaving a PDF-only trigger keeps the other conditions");
  ({ rule, stash } = chooseTrigger({ ...rule, conditions: [...rule.conditions, { type: "kind_is", kind: "notebooks" }] }, "read_progress", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "tag_is", tag: "keep" }], "entering a PDF-only trigger drops a kind_is");
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(rule.when.trigger.tag, "todo", "the page draft comes back");
  assert.deepEqual(rule.when.in, [{ id: "proj", kind: "folder" }]);
});

test("chooseAction toggles the page action and ignores cards that do not fit", () => {
  const p = newPageRule(DEFAULTS);
  const notes = chooseAction(p, "notes");
  assert.deepEqual(notes.actions, [{ type: "save_notes", prompt: "DEFAULT", model: "m/default" }]);
  assert.equal(actionKey(notes), "notes");
  assert.equal(actionKey(chooseAction(notes, "extract")), "extract");
  assert.equal(chooseAction(p, "move"), p);
  const m = newRule(FOLDERS);
  assert.equal(chooseAction(m, "notes"), m);
  assert.equal(actionKey(m), "move");
});

test("taggedCount counts pages in the rule's places and kind", () => {
  const tags = [{ name: "todo", pages: 4, docs: [
    { id: "d-ideas", folders: ["proj", ""], kind: "notebooks", pages: 2 },
    { id: "paper", folders: ["inb", ""], kind: "pdf_epub", pages: 1 },
    { id: "deep", folders: ["alpha", "proj", ""], kind: "notebooks", pages: 1 },
  ] }];
  const base = newPageRule(DEFAULTS);
  const r = { ...base, when: { ...base.when, trigger: { type: "page_tagged", tag: " TODO " } } };
  const inn = (places) => ({ ...r, when: { ...r.when, in: places } });
  assert.equal(taggedCount(tags, r), 4);
  assert.equal(taggedCount(tags, inn([{ id: "proj", kind: "folder" }])), 3);
  assert.equal(taggedCount(tags, inn([{ id: "paper", kind: "document" }])), 1);
  assert.equal(taggedCount(tags, { ...r, conditions: [{ type: "kind_is", kind: "notebooks" }] }), 3);
  assert.equal(taggedCount(tags, { ...r, when: { ...r.when, trigger: { type: "page_tagged", tag: "other" } } }), 0);
  assert.equal(taggedCount([], r), 0);
});

test("page tags normalise like document tags; suggestions keep one spelling", () => {
  for (const v of ["To-Do", "to_do", "  to   do ", "TO--DO"]) assert.equal(normalizePageTag(v), "to do");
  assert.deepEqual(tagSuggestions([{ name: "to do" }, { name: "To-Do" }, { name: "asset mgmt" }, { name: "" }]), ["to do", "asset mgmt"]);
  const tags = [{ name: "to do", docs: [{ id: "d", folders: [""], kind: "notebooks", pages: 2 }] }];
  const base = newPageRule(DEFAULTS);
  assert.equal(taggedCount(tags, { ...base, when: { ...base.when, trigger: { type: "page_tagged", tag: "To_Do" } } }), 2);
});

test("a root entry among other places renders as anywhere and stays editable as stored", () => {
  const base = newPageRule(DEFAULTS);
  const places = [{ id: "", kind: "folder", name: "My files" }, { id: "d-ideas", kind: "document", name: "Ideas" }];
  const r = { ...base, when: { ...base.when, in: places } };
  assert.equal(sentenceText(r, CTX), "IF I write on a page anywhere THEN extract tasks & annotations");
  assert.deepEqual(editableIn(r), places);
  assert.deepEqual(editableIn({ ...r, when: { ...r.when, in: [{ id: "", kind: "folder" }] } }), []);
  const tags = [{ name: "todo", docs: [{ id: "x", folders: ["other", ""], kind: "notebooks", pages: 3 }] }];
  assert.equal(taggedCount(tags, { ...r, when: { ...r.when, trigger: { type: "page_tagged", tag: "todo" } } }), 3);
});

test("switching triggers and back keeps each trigger's parameters", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let rule = GOLDEN_MOVE;
  let stash = null;
  const tr = () => rule.when.trigger;
  const setTr = (patch) => ({ ...rule, when: { ...rule.when, trigger: { ...rule.when.trigger, ...patch } } });
  ({ rule, stash } = chooseTrigger(rule, "read_progress", stash, opts));
  assert.equal(tr().percent, 80);
  rule = setTr({ percent: 55 });
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.deepEqual(tr(), GOLDEN_MOVE.when.trigger, "minPages 2, settle 6 kept");
  ({ rule, stash } = chooseTrigger(rule, "read_progress", stash, opts));
  assert.equal(tr().percent, 55);
  // the page tag survives page_changed and a trip through a document trigger
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  rule = setTr({ tag: "todo" });
  ({ rule, stash } = chooseTrigger(rule, "page_changed", stash, opts));
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(tr().tag, "todo");
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.equal(tr().minPages, 2);
  ({ rule, stash } = chooseTrigger(rule, "page_tagged", stash, opts));
  assert.equal(tr().tag, "todo");
});

test("ruleLabel: its own name, else its sentence", () => {
  assert.equal(ruleLabel(GOLDEN_PAGE, CTX), "journal pages");
  assert.equal(ruleLabel(GOLDEN_MOVE, CTX), "IF I finish reading a document in Reading List THEN move it to Done");
});
