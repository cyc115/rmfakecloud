// The conditions "+ add a condition" offers, mirroring the server catalogue
// (internal/automation/catalogue.go). The server is the authority on
// validation; this file names, phrases and defaults them for the editor.
// It is a leaf module: rules.js imports it, never the other way round.

export const KIND_LABELS = { notebooks: "only notebooks", pdf_epub: "only PDFs & books" };

// type, the subjects it applies to, its label in the add list, its params
// (the catalogue field names) and whether a rule may carry it more than once
export const CONDITIONS = [
  { type: "kind_is", subjects: ["document", "page"], label: "only notebooks / PDFs & books", params: ["kind"] },
  { type: "tag_is", subjects: ["document", "page"], label: "tagged …", params: ["tag"], repeatable: true },
  { type: "tag_is_not", subjects: ["document", "page"], label: "not tagged …", params: ["tag"], repeatable: true },
  { type: "not_opened_for_days", subjects: ["document"], label: "not opened for N days", params: ["days"] },
  { type: "min_age_days", subjects: ["document"], label: "at least N days old", params: ["days"] },
  { type: "name_matches", subjects: ["document", "page"], label: "named like …", params: ["pattern"] },
  { type: "pinned_is", subjects: ["document"], label: "pinned / not pinned", params: ["pinned"] },
  { type: "page_tag_is", subjects: ["page"], label: "on pages tagged …", params: ["tag"], repeatable: true },
];

const DEFAULTS = {
  kind_is: { kind: "notebooks" },
  tag_is: { tag: "" },
  tag_is_not: { tag: "" },
  not_opened_for_days: { days: 30 },
  min_age_days: { days: 7 },
  name_matches: { pattern: "" },
  pinned_is: { pinned: true },
  page_tag_is: { tag: "" },
};

// the triggers that read the page you stopped on, which only PDFs and books
// report: their kind_is is fixed. Titles as on the IF cards (rules.js).
const PDF_ONLY = { finished_reading: "I finish reading", read_progress: "I've read X%" };

export function conditionSpec(type) {
  return CONDITIONS.find((c) => c.type === type);
}

// isPdfOnly the trigger works on PDFs & books only (kind_is locked to pdf_epub)
export function isPdfOnly(triggerType) {
  return Object.prototype.hasOwnProperty.call(PDF_ONLY, triggerType);
}

// conditionLock the reason kind_is is fixed, or "" when it is free
export function conditionLock(triggerType) {
  if (!isPdfOnly(triggerType)) return "";
  return `Fixed: “${PDF_ONLY[triggerType]}” uses the page you stopped on, which only PDFs & books report.`;
}

// conditionsFor the condition types a rule of subject can carry, in catalogue order
export function conditionsFor(subject) {
  return CONDITIONS.filter((c) => c.subjects.includes(subject)).map((c) => c.type);
}

// conditionFit whether "+ add a condition" offers type for rule, and why not
export function conditionFit(type, rule) {
  const spec = conditionSpec(type);
  const subject = rule && rule.when && rule.when.subject;
  if (!spec) return { ok: false, reason: `Unknown condition “${type}”.` };
  if (!spec.subjects.includes(subject)) {
    return { ok: false, reason: subject === "page" ? "Works on documents, not pages." : "Works on pages, not documents." };
  }
  const trigger = (rule.when.trigger || {}).type;
  if (type === "kind_is" && isPdfOnly(trigger)) return { ok: false, reason: conditionLock(trigger) };
  // a kind_is with no kind (all) is no condition: forSave drops it
  const has = (c) => c.type === type && !(type === "kind_is" && !KIND_LABELS[c.kind]);
  if (!spec.repeatable && (rule.conditions || []).some(has)) {
    return { ok: false, reason: "This rule has it already." };
  }
  return { ok: true, reason: "" };
}

// makeCondition a new condition of type with editable defaults
export function makeCondition(type) {
  return { type, ...(DEFAULTS[type] || {}) };
}

// conditionPhrase the condition's words in the rule sentence; "" for kind_is all
export function conditionPhrase(cond) {
  const q = (v) => `“${v || "…"}”`;
  switch (cond.type) {
    case "kind_is": return KIND_LABELS[cond.kind] || "";
    case "tag_is": return `tagged ${q(cond.tag)}`;
    case "tag_is_not": return `not tagged ${q(cond.tag)}`;
    case "not_opened_for_days": return `not opened for ${cond.days} days`;
    case "min_age_days": return `at least ${cond.days} days old`;
    case "name_matches": return `named like ${q(cond.pattern)}`;
    case "pinned_is": return cond.pinned ? "pinned" : "not pinned";
    case "page_tag_is": return `on pages tagged ${q(cond.tag)}`;
    default: return cond.type;
  }
}
