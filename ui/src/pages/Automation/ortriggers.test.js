// Run: cd ui && npm test
// "or" triggers on document rules: when.orTriggers, any one of them fires the rule.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  addOrTrigger, chooseTrigger, forSave, orTriggersOf, removeOrTrigger, rulePdfOnly, sentenceText, setOrTrigger,
} from "./rules.js";
import { conditionFit } from "./conditions.js";

const CTX = { folders: [{ id: "", name: "My files", path: "My files" }, { id: "rr", name: "Research Reports", path: "Research Reports" }], watchables: [] };

// the live "finished reading in Research Reports -> Read" rule
const READ = {
  id: "r1", name: "", enabled: true,
  when: { subject: "document", in: [{ id: "rr", kind: "folder", name: "Research Reports" }],
    trigger: { type: "finished_reading", minPages: 2, settleHours: 6 } },
  conditions: [{ type: "kind_is", kind: "pdf_epub" }],
  actions: [{ type: "move", to: { id: "", createIfMissing: { name: "Read", parent: "source" } } }],
};

test("addOrTrigger offers “I tag a document” first, then a trigger the rule lacks", () => {
  const r = addOrTrigger(READ);
  assert.deepEqual(orTriggersOf(r), [{ type: "tag_is", tag: "" }]);
  const r2 = addOrTrigger(r);
  assert.equal(orTriggersOf(r2).length, 2);
  assert.ok(!["finished_reading", "tag_is"].includes(orTriggersOf(r2)[1].type));
  assert.equal(READ.when.orTriggers, undefined, "input not mutated");
});

test("the sentence reads both triggers and keeps the kind", () => {
  const r = setOrTrigger(addOrTrigger(READ), 0, { tag: "read" });
  assert.equal(sentenceText(r, CTX),
    "IF I finish reading a document OR I tag a document “read” in Research Reports (only PDFs & books) THEN move it to Read");
  assert.equal(sentenceText(READ, CTX), "IF I finish reading a document in Research Reports THEN move it to Read");
});

test("forSave: or tags trimmed, kind kept as stored, the key dropped when empty", () => {
  const r = setOrTrigger(addOrTrigger(READ), 0, { tag: "  read " });
  const out = forSave(r);
  assert.deepEqual(out.when.orTriggers, [{ type: "tag_is", tag: "read" }]);
  assert.deepEqual(out.conditions, [{ type: "kind_is", kind: "pdf_epub" }]);
  // mixed triggers leave the kind free: without kind_is it is sent without one
  const any = forSave({ ...r, conditions: [] });
  assert.deepEqual(any.conditions, []);
  const back = forSave(removeOrTrigger(r, 0));
  assert.equal("orTriggers" in back.when, false);
  assert.deepEqual(back.conditions, [{ type: "kind_is", kind: "pdf_epub" }], "all PDF-only again: kind fixed");
});

test("the kind is only locked while every trigger is PDF-only", () => {
  assert.equal(rulePdfOnly(READ), true);
  assert.equal(conditionFit("kind_is", { ...READ, conditions: [] }).ok, false);
  const mixed = { ...addOrTrigger(READ), conditions: [] };
  assert.equal(rulePdfOnly(mixed), false);
  assert.equal(conditionFit("kind_is", mixed).ok, true);
  const both = setOrTrigger(addOrTrigger(READ), 0, "read_progress");
  assert.equal(rulePdfOnly(both), true);
});

test("setOrTrigger: a new type starts from its defaults, the same type keeps its params", () => {
  let r = setOrTrigger(addOrTrigger(READ), 0, { tag: "read" });
  assert.deepEqual(orTriggersOf(setOrTrigger(r, 0, "tag_is"))[0], { type: "tag_is", tag: "read" });
  r = setOrTrigger(r, 0, "not_opened_for_days");
  assert.deepEqual(orTriggersOf(r)[0], { type: "not_opened_for_days", days: 30 });
});

test("page rules have no or-triggers; switching to a page trigger and back keeps them", () => {
  const r = setOrTrigger(addOrTrigger(READ), 0, { tag: "read" });
  const toPage = chooseTrigger(r, "page_changed", {}, { defaults: { prompt: "p", model: "m" }, folders: CTX.folders });
  assert.deepEqual(orTriggersOf(toPage.rule), []);
  const back = chooseTrigger(toPage.rule, "finished_reading", toPage.stash, { folders: CTX.folders });
  assert.deepEqual(orTriggersOf(back.rule), [{ type: "tag_is", tag: "read" }]);
});

test("switching the main trigger on a mixed rule keeps a chosen kind", () => {
  const mixed = setOrTrigger(addOrTrigger(READ), 0, { tag: "read" });
  // main finished_reading -> not_opened_for_days: neither before nor after all PDF-only
  const next = chooseTrigger(mixed, "name_matches", {}, { folders: CTX.folders }).rule;
  assert.deepEqual(next.conditions, [{ type: "kind_is", kind: "pdf_epub" }]);
});

test("adding an or trigger keeps a PDF-only rule's kind, now as a removable condition", () => {
  const fresh = { ...READ, conditions: [] }; // as the editor holds it: kind implied by the trigger
  const r = addOrTrigger(fresh);
  assert.deepEqual(r.conditions, [{ type: "kind_is", kind: "pdf_epub" }]);
  assert.equal(conditionFit("kind_is", r).ok, false, "has it already");
  assert.deepEqual(forSave(r).conditions, [{ type: "kind_is", kind: "pdf_epub" }]);
  // the user removes it: notebooks too
  assert.deepEqual(forSave({ ...r, conditions: [] }).conditions, []);
});
