// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { forSave, missingFor, pendingText, runCounts, newPipelineRule, newRule, switchAction } from "./rules.js";

test("newPipelineRule has page_changed and empty watch", () => {
  const r = newPipelineRule({ prompt: "P", model: "m/x" });
  assert.equal(r.trigger.type, "page_changed");
  assert.deepEqual(r.watch, []);
  assert.equal(r.action.type, "pipeline");
  assert.equal(r.action.prompt, "P");
  assert.equal(r.action.model, "m/x");
  assert.equal(r.scope, "all");
  assert.equal(r.enabled, true);
});

const FOLDERS = [{ id: "", name: "My files", path: "My files" }, { id: "f1", name: "Work", path: "Work" }];
const DEFAULTS = { prompt: "DEFAULT", model: "m/default" };

test("switchAction pipeline → move drops watch", () => {
  const pipe = { ...newPipelineRule(DEFAULTS), id: "r1", watch: [{ id: "f1", kind: "folder" }] };
  const { rule } = switchAction(pipe, "move", null, { defaults: DEFAULTS, folders: FOLDERS });
  assert.equal(rule.action.type, "move");
  assert.equal("watch" in rule, false);
  assert.equal(rule.id, "r1");
});

test("switchAction move → pipeline → move → pipeline keeps the edited prompt and the move fields", () => {
  const opts = { defaults: DEFAULTS, folders: FOLDERS };
  const move = { ...newRule(FOLDERS), id: "r1", name: "n" };
  let { rule, stash } = switchAction(move, "pipeline", null, opts);
  assert.equal(rule.action.prompt, "DEFAULT");
  rule = { ...rule, action: { ...rule.action, prompt: "EDITED" }, watch: [{ id: "f1", kind: "folder" }] };
  ({ rule, stash } = switchAction(rule, "move", stash, opts));
  assert.deepEqual(rule.source, move.source);
  assert.equal("watch" in rule, false);
  ({ rule, stash } = switchAction(rule, "pipeline", stash, opts));
  assert.equal(rule.action.prompt, "EDITED");
  assert.deepEqual(rule.watch, [{ id: "f1", kind: "folder" }]);
  assert.equal(rule.name, "n");
});

test("forSave trims prompt and model; whitespace-only becomes empty", () => {
  const r = { ...newPipelineRule(DEFAULTS), action: { type: "pipeline", prompt: "  \n ", model: " m/x \n" } };
  const out = forSave(r);
  assert.equal(out.action.prompt, "");
  assert.equal(out.action.model, "m/x");
  assert.equal(r.action.model, " m/x \n", "input not mutated");
  assert.equal("watch" in forSave({ ...newRule(FOLDERS), watch: [] }), false);
});

test("forSave: anywhere is the root folder entry for both page triggers", () => {
  const root = [{ id: "", kind: "folder", name: "My files" }];
  const pc = forSave(newPipelineRule(DEFAULTS));
  assert.deepEqual(pc.watch, root);
  const pt = forSave({ ...newPipelineRule(DEFAULTS), trigger: { type: "page_tagged", tag: " todo " }, watch: [] });
  assert.deepEqual(pt.watch, root);
  assert.equal(pt.trigger.tag, "todo");
  // a root entry next to others is kept as stored (it still means anywhere)
  const mixed = [{ id: "", kind: "folder" }, { id: "d1", kind: "document", name: "Notes" }];
  assert.deepEqual(forSave({ ...newPipelineRule(DEFAULTS), watch: mixed }).watch, mixed);
  const some = [{ id: "d1", kind: "document", name: "Notes" }];
  assert.deepEqual(forSave({ ...newPipelineRule(DEFAULTS), watch: some }).watch, some);
});

test("forSave: tasks false is kept, true is dropped, never on a move rule", () => {
  const notes = forSave({ ...newPipelineRule(DEFAULTS), action: { type: "pipeline", tasks: false } });
  assert.equal(notes.action.tasks, false);
  const extract = forSave({ ...newPipelineRule(DEFAULTS), action: { type: "pipeline", tasks: true } });
  assert.equal("tasks" in extract.action, false);
  const move = forSave({ ...newRule(FOLDERS), action: { type: "move", tasks: false } });
  assert.equal("tasks" in move.action, false);
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
