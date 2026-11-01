// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { ACTIONS, newPipelineRule, pipelineSummary, triggerPhrase } from "./rules.js";

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
