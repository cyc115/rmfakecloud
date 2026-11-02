// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { CONDITIONS, conditionFit, conditionPhrase, conditionsFor, makeCondition } from "./conditions.js";

const docRule = (trigger, conditions = []) => ({
  id: "", name: "", enabled: true,
  when: { subject: "document", in: [{ id: "f1", kind: "folder", name: "Inbox" }], trigger },
  conditions, actions: [{ type: "move", to: { id: "f2", name: "Done" } }],
});
const pageRule = (conditions = []) => ({
  id: "", name: "", enabled: true,
  when: { subject: "page", in: [], trigger: { type: "page_changed" } },
  conditions, actions: [{ type: "extract_tasks", prompt: "", model: "" }],
});

test("conditionsFor lists each subject's conditions in catalogue order", () => {
  assert.deepEqual(conditionsFor("page"), ["kind_is", "tag_is", "tag_is_not", "name_matches", "page_tag_is"]);
  assert.deepEqual(conditionsFor("document"), ["kind_is", "tag_is", "tag_is_not", "not_opened_for_days", "min_age_days", "name_matches", "pinned_is"]);
});

test("every catalogue entry has type, subjects, label and params", () => {
  for (const c of CONDITIONS) {
    assert.equal(typeof c.type, "string");
    assert.ok(c.subjects.length > 0, c.type);
    assert.ok(c.label, c.type);
    assert.ok(Array.isArray(c.params), c.type);
    const made = makeCondition(c.type);
    assert.equal(made.type, c.type);
    assert.deepEqual(Object.keys(made).filter((k) => k !== "type").sort(), [...c.params].sort(), c.type);
  }
});

test("kind_is is fixed on a PDF-only trigger, with A's reason", () => {
  const finishedReadingRule = docRule({ type: "finished_reading", minPages: 3, settleHours: 6 });
  const fit = conditionFit("kind_is", finishedReadingRule);
  assert.equal(fit.ok, false);
  assert.equal(fit.reason, "Fixed: “I finish reading” uses the page you stopped on, which only PDFs & books report.");
  assert.match(conditionFit("kind_is", docRule({ type: "read_progress", percent: 80 })).reason, /^Fixed: “I've read X%”/);
  assert.equal(conditionFit("kind_is", docRule({ type: "name_matches", pattern: "x" })).ok, true);
  // the other conditions stay open on a PDF-only trigger
  assert.equal(conditionFit("tag_is", finishedReadingRule).ok, true);
});

test("a repeat of a non-repeatable type is not offered; repeatable ones are", () => {
  const r = docRule({ type: "not_opened_for_days", days: 30 }, [
    { type: "kind_is", kind: "notebooks" }, { type: "min_age_days", days: 7 }, { type: "tag_is", tag: "keep" },
  ]);
  for (const t of ["kind_is", "min_age_days"]) {
    const fit = conditionFit(t, r);
    assert.equal(fit.ok, false, t);
    assert.ok(fit.reason, t);
  }
  assert.equal(conditionFit("tag_is", r).ok, true, "tag_is repeats");
  assert.equal(conditionFit("tag_is_not", r).ok, true);
  const p = pageRule([{ type: "page_tag_is", tag: "a" }, { type: "name_matches", pattern: "J*" }]);
  assert.equal(conditionFit("page_tag_is", p).ok, true, "page_tag_is repeats");
  assert.equal(conditionFit("name_matches", p).ok, false);
  // a kind_is all is no condition, so a real kind is still offered
  assert.equal(conditionFit("kind_is", pageRule([{ type: "kind_is", kind: "all" }])).ok, true);
});

test("a condition of the other subject is never offered", () => {
  assert.equal(conditionFit("page_tag_is", docRule({ type: "tag_is", tag: "x" })).ok, false);
  for (const t of ["not_opened_for_days", "min_age_days", "pinned_is"]) assert.equal(conditionFit(t, pageRule()).ok, false, t);
  assert.equal(conditionFit("no_such_type", pageRule()).ok, false);
});

test("conditionPhrase renders each condition", () => {
  const cases = [
    [{ type: "kind_is", kind: "notebooks" }, "only notebooks"],
    [{ type: "kind_is", kind: "pdf_epub" }, "only PDFs & books"],
    [{ type: "tag_is", tag: "x" }, "tagged “x”"],
    [{ type: "tag_is_not", tag: "x" }, "not tagged “x”"],
    [{ type: "not_opened_for_days", days: 30 }, "not opened for 30 days"],
    [{ type: "min_age_days", days: 7 }, "at least 7 days old"],
    [{ type: "name_matches", pattern: "p" }, "named like “p”"],
    [{ type: "pinned_is", pinned: true }, "pinned"],
    [{ type: "pinned_is", pinned: false }, "not pinned"],
    [{ type: "page_tag_is", tag: "x" }, "on pages tagged “x”"],
    [{ type: "tag_is", tag: "" }, "tagged “…”"],
  ];
  for (const [c, want] of cases) assert.equal(conditionPhrase(c), want);
});

test("makeCondition gives editable defaults", () => {
  assert.deepEqual(makeCondition("kind_is"), { type: "kind_is", kind: "notebooks" });
  assert.deepEqual(makeCondition("min_age_days"), { type: "min_age_days", days: 7 });
  assert.deepEqual(makeCondition("not_opened_for_days"), { type: "not_opened_for_days", days: 30 });
  assert.deepEqual(makeCondition("pinned_is"), { type: "pinned_is", pinned: true });
  assert.deepEqual(makeCondition("page_tag_is"), { type: "page_tag_is", tag: "" });
});
