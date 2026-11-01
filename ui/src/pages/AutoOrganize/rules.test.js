// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { ACTIONS, forSave, missingFor, pendingText, runCounts, newPipelineRule, newRule, pipelineSummary, switchAction, triggerPhrase } from "./rules.js";

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

test("triggerPhrase(page_changed)", () => {
  assert.equal(triggerPhrase({ type: "page_changed" }), "a watched page changes");
});

test("pipelineSummary counts watched items", () => {
  assert.equal(pipelineSummary({ watch: [] }), "Watching 0 items → pipeline");
  assert.equal(pipelineSummary({ watch: [{ id: "a" }] }), "Watching 1 item → pipeline");
  assert.equal(pipelineSummary({ watch: [{ id: "a" }, { id: "b" }] }), "Watching 2 items → pipeline");
});

test("ACTIONS lists move and pipeline", () => {
  assert.deepEqual(ACTIONS.map((a) => a.type), ["move", "pipeline"]);
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

test("missingFor filters by rule", () => {
  const p = { missingWatches: [{ ruleId: "a", watchId: "x" }, { ruleId: "b", watchId: "y" }] };
  assert.deepEqual(missingFor(p, "a"), [{ ruleId: "a", watchId: "x" }]);
  assert.deepEqual(missingFor(null, "a"), []);
  assert.deepEqual(missingFor({}, "a"), []);
});

test("pendingText says Dry-run holds the changes in dry mode", () => {
  assert.equal(pendingText(0, false), "");
  assert.match(pendingText(3, false), /^3 page changes held by Dry-run mode/);
  assert.doesNotMatch(pendingText(3, false), /rm-pages/);
  assert.equal(pendingText(1, true), "1 page change waiting to be accepted by rm-pages.");
});

test("runCounts never counts feed events as pipeline sent", () => {
  assert.equal(runCounts({ mode: "live", accepted: 5, pipelineAccepted: 2, pipeline: 2 }), "3 page changes sent, 2 to the pipeline");
  assert.equal(runCounts({ mode: "dry", accepted: 1, pipelineAccepted: 0, pipeline: 4 }), "1 page change sent, 0 to the pipeline (4 held by Dry-run)");
  assert.equal(runCounts({ mode: "live", accepted: 0, pipeline: 0 }), "0 page changes sent, 0 to the pipeline");
});
