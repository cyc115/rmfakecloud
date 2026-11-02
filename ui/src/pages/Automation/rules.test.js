// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  actionKey, chooseTrigger, forSave, isPageRule, missingFor, newPageRule, newRule, pendingText, runCounts, taggedCount, taggedNow, withDisplayNames,
} from "./rules.js";
import { readFileSync } from "node:fs";

const FOLDERS = [{ id: "", name: "My files", path: "My files" }, { id: "f1", name: "Work", path: "Work" }, { id: "f2", name: "Done", path: "Work / Done" }];
const DEFAULTS = { prompt: "DEFAULT", model: "m/default" };
const ROOT_IN = [{ id: "", kind: "folder", name: "My files" }];

const page = (patch = {}) => {
  const r = newPageRule(DEFAULTS);
  return { ...r, ...patch, when: { ...r.when, ...(patch.when || {}) } };
};

test("newPageRule: page_changed, empty in, one extract action with the server defaults", () => {
  const r = newPageRule({ prompt: "P", model: "m/x" });
  assert.equal(isPageRule(r), true);
  assert.deepEqual(r.when, { subject: "page", in: [], trigger: { type: "page_changed" } });
  assert.deepEqual(r.conditions, []);
  assert.deepEqual(r.actions, [{ type: "extract_tasks", prompt: "P", model: "m/x" }]);
  assert.equal(r.enabled, true);
  assert.equal(r.name, "");
});

test("newRule: one folder in, a move action", () => {
  const r = newRule(FOLDERS);
  assert.equal(isPageRule(r), false);
  assert.deepEqual(r.when.in, [{ id: "f1", kind: "folder", name: "Work" }]);
  assert.equal(actionKey(r), "move");
  assert.equal(r.actions.length, 1);
});

test("forSave: an empty page in becomes the root entry, for both page triggers", () => {
  assert.deepEqual(forSave(page()).when.in, ROOT_IN);
  const pt = forSave(page({ when: { trigger: { type: "page_tagged", tag: " todo " } } }));
  assert.deepEqual(pt.when.in, ROOT_IN);
  assert.equal(pt.when.trigger.tag, "todo");
  // a root entry next to others is kept as stored (it still means anywhere)
  const mixed = [{ id: "", kind: "folder" }, { id: "d1", kind: "document", name: "Notes" }];
  assert.deepEqual(forSave(page({ when: { in: mixed } })).when.in, mixed);
  const some = [{ id: "d1", kind: "document", name: "Notes" }];
  assert.deepEqual(forSave(page({ when: { in: some } })).when.in, some);
});

test("forSave drops kind_is all and blank, keeps a real kind", () => {
  for (const kind of ["all", ""]) {
    const out = forSave(page({ conditions: [{ type: "kind_is", kind }, { type: "name_matches", pattern: "J*" }] }));
    assert.deepEqual(out.conditions, [{ type: "name_matches", pattern: "J*" }], kind);
  }
  const nb = forSave(page({ conditions: [{ type: "kind_is", kind: "notebooks" }] }));
  assert.deepEqual(nb.conditions, [{ type: "kind_is", kind: "notebooks" }]);
});

test("forSave adds kind_is pdf_epub for a PDF-only trigger, once", () => {
  const r = { ...newRule(FOLDERS), conditions: [{ type: "tag_is", tag: "keep" }] };
  assert.deepEqual(forSave(r).conditions, [{ type: "kind_is", kind: "pdf_epub" }, { type: "tag_is", tag: "keep" }]);
  const stored = { ...r, conditions: [{ type: "kind_is", kind: "pdf_epub" }, { type: "tag_is", tag: "keep" }] };
  assert.deepEqual(forSave(stored).conditions, forSave(r).conditions);
  const nm = { ...r, when: { ...r.when, trigger: { type: "name_matches", pattern: "x" } } };
  assert.deepEqual(forSave(nm).conditions, [{ type: "tag_is", tag: "keep" }]);
});

test("forSave sends exactly one action, trimmed for pages, without prompt on a move", () => {
  const two = page({ actions: [{ type: "save_notes", prompt: "  \n ", model: " m/x \n" }, { type: "extract_tasks" }] });
  const out = forSave(two);
  assert.equal(out.actions.length, 1);
  assert.deepEqual(out.actions[0], { type: "save_notes", prompt: "", model: "m/x" });
  assert.equal(two.actions[0].model, " m/x \n", "input not mutated");
  const move = forSave({ ...newRule(FOLDERS), actions: [{ type: "move", to: { id: "f2", name: "Done" }, prompt: "x", model: "y" }] });
  assert.deepEqual(move.actions, [{ type: "move", to: { id: "f2", name: "Done" } }]);
  assert.equal(forSave({ ...page(), actions: [] }).actions.length, 1);
  assert.equal(forSave({ ...newRule(FOLDERS), actions: [] }).actions.length, 1);
  for (const r of [page(), newRule(FOLDERS)]) assert.equal(forSave(r).actions.length, 1);
});

test("forSave trims page_tag_is tags", () => {
  const out = forSave(page({ conditions: [{ type: "page_tag_is", tag: "  urgent " }] }));
  assert.deepEqual(out.conditions, [{ type: "page_tag_is", tag: "urgent" }]);
});

test("chooseTrigger from page to document and back restores in, prompt and conditions", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  const draft = page({
    id: "r1", name: "n",
    when: { in: [{ id: "d1", kind: "document", name: "Notes" }] },
    conditions: [{ type: "name_matches", pattern: "J*" }],
    actions: [{ type: "extract_tasks", prompt: "EDITED", model: "m/x" }],
  });
  let { rule, stash } = chooseTrigger(draft, "not_opened_for_days", null, opts);
  assert.equal(rule.when.subject, "document");
  assert.equal(rule.id, "r1");
  assert.equal(rule.name, "n");
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 7 }], "A's default min age comes with the first pick");
  assert.equal(actionKey(rule), "move");
  rule = { ...rule, conditions: [{ type: "min_age_days", days: 3 }] };
  ({ rule, stash } = chooseTrigger(rule, "page_changed", stash, opts));
  assert.deepEqual(rule.when.in, draft.when.in);
  assert.deepEqual(rule.actions, draft.actions);
  assert.deepEqual(rule.conditions, draft.conditions);
  ({ rule, stash } = chooseTrigger(rule, "not_opened_for_days", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 3 }], "the document draft comes back too");
});

test("the min age default is added once: a removed chip stays removed", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let { rule, stash } = chooseTrigger(newRule(FOLDERS), "not_opened_for_days", null, opts);
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 7 }]);
  rule = { ...rule, conditions: [] };
  ({ rule, stash } = chooseTrigger(rule, "name_matches", stash, opts));
  ({ rule, stash } = chooseTrigger(rule, "not_opened_for_days", stash, opts));
  assert.deepEqual(rule.conditions, []);
});

test("leaving “It sits unopened” drops the untouched auto min age (P1), and it comes back with it", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let { rule, stash } = chooseTrigger(newRule(FOLDERS), "not_opened_for_days", null, opts);
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.deepEqual(rule.conditions, [], "a reading rule has no min age");
  ({ rule, stash } = chooseTrigger(rule, "not_opened_for_days", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 7 }]);
  // through a page trigger and back to a document trigger: still dropped
  ({ rule, stash } = chooseTrigger(rule, "page_changed", stash, opts));
  ({ rule, stash } = chooseTrigger(rule, "age_since_created", stash, opts));
  assert.deepEqual(rule.conditions, []);
});

test("a min age the user edited or added is kept when leaving “It sits unopened”", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  let { rule, stash } = chooseTrigger(newRule(FOLDERS), "not_opened_for_days", null, opts);
  rule = { ...rule, conditions: rule.conditions.map((c) => ({ ...c, days: 3 })) };
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 3 }], "edited");
  // a stored rule's min age (not added by chooseTrigger) is the user's, even at 7 days
  const stored = { ...newRule(FOLDERS), when: { ...newRule(FOLDERS).when, trigger: { type: "not_opened_for_days", days: 30 } },
    conditions: [{ type: "min_age_days", days: 7 }] };
  ({ rule } = chooseTrigger(stored, "finished_reading", null, opts));
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 7 }], "stored");
  // removed, then added back by hand at 7 days: the user's
  ({ rule, stash } = chooseTrigger(newRule(FOLDERS), "not_opened_for_days", null, opts));
  rule = { ...rule, conditions: [] };
  rule = { ...rule, conditions: [{ type: "min_age_days", days: 7 }] };
  ({ rule, stash } = chooseTrigger(rule, "finished_reading", stash, opts));
  assert.deepEqual(rule.conditions, [{ type: "min_age_days", days: 7 }], "re-added by hand");
});

test("taggedCount: two page tags give null (per-document counts cannot say which pages carry both)", () => {
  const tags = [
    { name: "a", docs: [{ id: "d1", folders: [""], kind: "notebooks", pages: 3 }, { id: "d2", folders: [""], kind: "notebooks", pages: 1 }] },
    { name: "b", docs: [{ id: "d1", folders: [""], kind: "notebooks", pages: 2 }, { id: "d3", folders: [""], kind: "notebooks", pages: 4 }] },
  ];
  const r = page({ when: { trigger: { type: "page_tagged", tag: "a" } }, conditions: [{ type: "page_tag_is", tag: "B" }] });
  assert.equal(taggedCount(tags, r), null);
  // page_changed with one page_tag_is runs the tagged path too (P4): one tag counts
  assert.equal(taggedCount(tags, page({ conditions: [{ type: "page_tag_is", tag: "b" }] })), 6);
  // the trigger tag repeated as a condition is still one tag
  assert.equal(taggedCount(tags, { ...r, conditions: [{ type: "page_tag_is", tag: "A" }] }), 4);
  // a condition the page-tag list cannot test leaves the count unknown
  const one = page({ when: { trigger: { type: "page_tagged", tag: "a" } } });
  assert.equal(taggedCount(tags, { ...one, conditions: [{ type: "name_matches", pattern: "J*" }] }), null);
  assert.equal(taggedCount(tags, page()), null, "no tag set: nothing to count");
});

test("taggedNow: the page-tag list when it can count, else the PagePreview's pages", () => {
  const tags = [{ name: "a", docs: [{ id: "d1", folders: [""], kind: "notebooks", pages: 3 }] }];
  const one = page({ when: { trigger: { type: "page_tagged", tag: "a" } } });
  const two = { ...one, conditions: [{ type: "page_tag_is", tag: "b" }] };
  assert.equal(taggedNow(tags, one, { pages: 9, docs: 1 }), 3, "the list counts one tag at once");
  assert.equal(taggedNow(tags, two, { pages: 2, docs: 1 }), 2, "two tags: the preview counts");
  assert.equal(taggedNow(tags, two, null), null, "two tags, preview not back yet");
  assert.equal(taggedNow(null, one, { pages: 5, docs: 2 }), 5, "list not loaded: the preview counts");
  assert.equal(taggedNow(tags, page(), { pages: 7, docs: 1 }), null, "no page tag: no tagged line");
});

test("forSave round-trips the migrated golden rules (v2-golden.json)", () => {
  const golden = JSON.parse(readFileSync(new URL("../../../../internal/autoorganize/testdata/v2-golden.json", import.meta.url), "utf8"));
  assert.ok(golden.rules.length >= 2);
  for (const r of golden.rules) assert.deepEqual(forSave(r), r, r.id);
  // by design: a page_tagged rule with an empty in is sent with the root entry
  const pageRule = golden.rules.find((r) => r.when.subject === "page");
  const tagged = { ...pageRule, when: { ...pageRule.when, in: [], trigger: { type: "page_tagged", tag: "todo" } } };
  assert.deepEqual(forSave(tagged), { ...tagged, when: { ...tagged.when, in: ROOT_IN } });
});

test("withDisplayNames caches the source and destination names", () => {
  const r = { ...newRule(FOLDERS), when: { ...newRule(FOLDERS).when, in: [{ id: "f1", kind: "folder", name: "stale" }] },
    actions: [{ type: "move", to: { id: "f2", name: "" } }] };
  const out = withDisplayNames(r, FOLDERS);
  assert.equal(out.when.in[0].name, "Work");
  assert.equal(out.actions[0].to.name, "Done");
  assert.equal(r.when.in[0].name, "stale", "input not mutated");
});

test("missingFor filters by rule", () => {
  const p = { missingWatches: [{ ruleId: "a", watchId: "x" }, { ruleId: "b", watchId: "y" }] };
  assert.deepEqual(missingFor(p, "a"), [{ ruleId: "a", watchId: "x" }]);
  assert.deepEqual(missingFor(null, "a"), []);
  assert.deepEqual(missingFor({}, "a"), []);
});

test("pendingText says Dry-run holds the changes in dry mode", () => {
  assert.equal(pendingText(0, false), "");
  assert.match(pendingText(3, false), /^3 page changes held by Dry-run mode/);
  assert.doesNotMatch(pendingText(3, false), /rm-pages|pipeline/i);
  assert.equal(pendingText(1, true), "1 page change waiting to be accepted.");
});

test("runCounts never counts feed events as rule events", () => {
  assert.equal(runCounts({ mode: "live", accepted: 5, pipelineAccepted: 2, pipeline: 2 }), "3 page changes to the timeline, 2 to your rules");
  assert.equal(runCounts({ mode: "dry", accepted: 1, pipelineAccepted: 0, pipeline: 4 }), "1 page change to the timeline, 0 to your rules (4 held by Dry-run)");
  assert.equal(runCounts({ mode: "live", accepted: 0, pipeline: 0 }), "0 page changes to the timeline, 0 to your rules");
});

test("api.service paths start with /automation", async () => {
  const urls = [];
  globalThis.fetch = async (url) => {
    urls.push(url);
    return { ok: true, status: 200, json: async () => ({}) };
  };
  const { default: api } = await import("../../services/api.service.js");
  const calls = [
    api.getAutomation(), api.saveAutomation({}), api.getAutomationState(), api.previewAutomation({}), api.runAutomationNow(),
    api.getAutomationLog(), api.undoAutomationMove("e1"), api.reapplyAutomationHold("h1"), api.getWatchables(), api.getPageTags(),
    api.getPipeline(), api.runPipelineNow(),
  ];
  await Promise.all(calls);
  assert.equal(urls.length, calls.length);
  for (const u of urls) assert.match(u, /^\/ui\/api\/automation(\/|$)/, u);
  assert.ok(urls.includes("/ui/api/automation/undo/e1"));
  assert.ok(urls.includes("/ui/api/automation/pipeline/run-now"));
});
