// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  actionKey, chooseTrigger, forSave, isPageRule, missingFor, newPageRule, newRule, pendingText, runCounts, taggedCount, withDisplayNames,
} from "./rules.js";

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

test("taggedCount: trigger tag a and a page_tag_is b count only pages carrying both", () => {
  const tags = [
    { name: "a", docs: [
      { id: "d1", folders: [""], kind: "notebooks", pages: 3, pageIds: ["p1", "p2", "p3"] },
      { id: "d2", folders: [""], kind: "notebooks", pages: 1, pageIds: ["q1"] },
    ] },
    { name: "b", docs: [
      { id: "d1", folders: [""], kind: "notebooks", pages: 2, pageIds: ["p2", "p9"] },
      { id: "d3", folders: [""], kind: "notebooks", pages: 4, pageIds: ["r1", "r2", "r3", "r4"] },
    ] },
  ];
  const r = page({ when: { trigger: { type: "page_tagged", tag: "a" } }, conditions: [{ type: "page_tag_is", tag: "B" }] });
  assert.equal(taggedCount(tags, r), 1, "only d1's p2 carries both");
  // page_changed with page_tag_is runs the tagged path too (P4)
  assert.equal(taggedCount(tags, page({ conditions: [{ type: "page_tag_is", tag: "b" }] })), 6);
  // without page ids the overlap is unknown: no number rather than a wrong one
  const counts = tags.map((t) => ({ ...t, docs: t.docs.map(({ pageIds, ...d }) => d) }));
  assert.equal(taggedCount(counts, r), null);
  // a condition the page-tag list cannot test leaves the count unknown
  assert.equal(taggedCount(tags, { ...r, conditions: [...r.conditions, { type: "name_matches", pattern: "J*" }] }), null);
  assert.equal(taggedCount(tags, page()), null, "no tag set: nothing to count");
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
