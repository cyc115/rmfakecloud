import { useEffect, useRef, useState } from "react";
import apiService from "../../services/api.service";
import FolderPicker from "./FolderPicker";
import PlacesPicker from "./PlacesPicker";
import Sentence from "./Sentence";
import { KIND_LABELS, TIME_UNITS, conditionFit, conditionPhrase, conditionSpec, conditionsFor, makeCondition } from "./conditions";
import {
  ACTION_CARDS, NO_AUTOMATION_TAG, PAGE_TAGGED, PDF_ONLY_BADGE, ROOT_NAME, TRIGGER_CARDS,
  actionFit, actionKey, actionOf, chooseAction, chooseTrigger, conditionLock, destinationName, editableIn, forSave, isPageRule, isTagAction, tagActionText,
  moveTo, normalizePageTag, pageTagsOf, patternError, placesOf, plural, ruleLabel, sentenceText, sourceOf, tagSuggestions, taggedNow,
} from "./rules";

function num(v) {
  const n = parseInt(v, 10);
  return Number.isNaN(n) ? 0 : n;
}

const isNoAutomation = (tag) => (tag || "").trim().toLowerCase().replace(/[\s_-]+/g, " ") === NO_AUTOMATION_TAG;

const isTimeType = (type) => type === "created_time" || type === "modified_time";

// TimeInputs "in the last / more than" N hours|days|weeks (ago), for the
// created_time and modified_time trigger and conditions
function TimeInputs({ value, label, focus, onChange }) {
  return (
    <>
      <select className="ao-inline-sel" value={value.compare} aria-label={`${label} compare`} autoFocus={focus}
        onChange={(e) => onChange({ compare: e.target.value })}>
        <option value="within">in the last</option>
        <option value="older">more than</option>
      </select>
      <input type="number" min={1} value={value.amount} aria-label={`${label} amount`}
        onChange={(e) => onChange({ amount: num(e.target.value) })} />
      <select className="ao-inline-sel" value={value.unit} aria-label={`${label} unit`}
        onChange={(e) => onChange({ unit: e.target.value })}>
        {TIME_UNITS.map((u) => <option key={u} value={u}>{u}</option>)}
      </select>
      {value.compare === "older" && <span className="n">ago</span>}
    </>
  );
}

// conditionError the inline error for one condition, or null
function conditionError(c) {
  switch (c.type) {
    case "tag_is":
    case "tag_is_not":
      if (!(c.tag || "").trim()) return `Enter a tag for “${conditionSpec(c.type).label}”.`;
      if (isNoAutomation(c.tag)) return "“no automation” documents are always skipped; pick another tag.";
      return null;
    // a blank page tag matches nothing, so it blocks saving
    case "page_tag_is": return normalizePageTag(c.tag) ? null : "Pick or type a page tag for “on pages tagged …”.";
    case "name_matches": return patternError(c.pattern);
    case "not_opened_for_days": return c.days < 1 ? "Days must be at least 1." : null;
    case "min_age_days": return c.days < 0 ? "Minimum age cannot be negative." : null;
    case "created_time":
    case "modified_time": return c.amount < 1 ? "The amount of time must be at least 1." : null;
    default: return null;
  }
}

// Client side checks shown inline; the server validates again on save.
function validate(rule, otherRules, folders) {
  const errors = [];
  const warnings = [];
  const add = (e) => e && !errors.includes(e) && errors.push(e);
  const t = rule.when.trigger;
  if (isPageRule(rule)) {
    if (t.type === PAGE_TAGGED && !normalizePageTag(t.tag)) add("Pick or type a page tag.");
    (rule.conditions || []).forEach((c) => add(conditionError(c)));
    return { errors, warnings };
  }
  const src = sourceOf(rule);
  const to = moveTo(rule);
  const cm = to.createIfMissing;
  const act = actionOf(rule);
  const tagging = isTagAction(act.type);
  // the pickers list only folders outside the Trash
  const exists = (id) => (folders || []).some((f) => f.id === id);
  if (!exists(src.id)) add("The folder in “In” is missing or in the Trash. Pick a folder.");
  if (tagging) {
    if (!(act.tag || "").trim()) add(`Enter the tag to ${act.type === "add_tag" ? "add" : "remove"}.`);
    else if (isNoAutomation(act.tag)) add("“no automation” documents are always skipped; pick another tag.");
  } else if (cm) {
    if (!cm.name.trim()) add("Name the new subfolder.");
  } else if (!exists(to.id)) {
    add("Destination folder missing or in Trash. Pick a folder.");
  } else if (to.id === src.id) {
    add("Destination is the same as the source. Pick a different folder.");
  }
  if (t.type === "name_matches") add(patternError(t.pattern));
  if (t.type === "tag_is" && !t.tag.trim()) add("Enter a tag.");
  if (t.type === "tag_is" && isNoAutomation(t.tag)) add("“no automation” documents are always skipped; pick another tag.");
  if (t.type === "finished_reading" && t.minPages < 1) add("Minimum pages must be at least 1.");
  if (t.type === "read_progress" && (t.percent < 1 || t.percent > 100)) add("Percent must be between 1 and 100.");
  if (["not_opened_for_days", "not_modified_for_days", "age_since_created"].includes(t.type) && t.days < 1) {
    add("Days must be at least 1.");
  }
  if (isTimeType(t.type) && t.amount < 1) add("The amount of time must be at least 1.");
  (rule.conditions || []).forEach((c) => add(conditionError(c)));
  if (!cm && !tagging) {
    const loops = otherRules.filter((o) => o.enabled && sourceOf(o).id === to.id &&
      !moveTo(o).createIfMissing && moveTo(o).id === src.id);
    if (loops.length) {
      warnings.push(`Loops with rule ${loops.map((o) => o._number).join(", ")}. The 24 h cycle guard limits this to one round trip.`);
    }
  }
  return { errors, warnings };
}

function Card({ card, selected, disabled, onPick, badge }) {
  return (
    <button type="button" role="radio" aria-checked={selected} aria-label={card.title} disabled={disabled}
      className={`ao-ccard ${selected ? "sel" : ""} ${disabled ? "dim" : ""}`} onClick={onPick}>
      <span className="ic" aria-hidden="true">{card.icon}</span>
      <span className="t">{card.title}</span>
      <span className="s">{card.sub}</span>
      {badge && <span className="ao-badge">{badge}</span>}
    </button>
  );
}

// ConditionChip one condition: its words, its parameter inputs, and ×
function ConditionChip({ cond, index, focus, onChange, onRemove }) {
  const phrase = conditionPhrase(cond);
  const lbl = (field) => `condition ${index + 1} ${field}`;
  const text = (field, placeholder, list) => (
    <input type="text" value={cond[field] || ""} placeholder={placeholder} aria-label={lbl(field)} list={list} autoFocus={focus}
      onChange={(e) => onChange({ [field]: e.target.value })} />
  );
  const days = (min) => (
    <input type="number" min={min} value={cond.days} aria-label={lbl("days")} autoFocus={focus}
      onChange={(e) => onChange({ days: num(e.target.value) })} />
  );
  let body;
  switch (cond.type) {
    case "kind_is": body = <span className="n">{phrase.replace(/^only /, "")}</span>; break;
    case "tag_is": body = <><span className="n">tagged</span>{text("tag", "tag name")}</>; break;
    case "tag_is_not": body = <><span className="n">not tagged</span>{text("tag", "tag name")}</>; break;
    case "not_opened_for_days": body = <><span className="n">not opened for</span>{days(1)}<span className="n">days</span></>; break;
    case "min_age_days": body = <><span className="n">at least</span>{days(0)}<span className="n">days old</span></>; break;
    case "name_matches": body = <><span className="n">named like</span>{text("pattern", "glob or /regex/")}</>; break;
    case "page_tag_is": body = <><span className="n">on pages tagged</span>{text("tag", "page tag", "ao-page-tags")}</>; break;
    case "created_time":
    case "modified_time":
      body = (
        <>
          <span className="n">{cond.type === "created_time" ? "created" : "changed"}</span>
          <TimeInputs value={cond} label={`condition ${index + 1}`} focus={focus} onChange={onChange} />
        </>
      );
      break;
    case "pinned_is":
      body = (
        <select className="ao-inline-sel" value={cond.pinned ? "1" : "0"} aria-label={lbl("pinned")}
          onChange={(e) => onChange({ pinned: e.target.value === "1" })}>
          <option value="1">pinned</option>
          <option value="0">not pinned</option>
        </select>
      );
      break;
    default: body = <span className="n">{phrase}</span>;
  }
  return (
    <span className={`ao-wchip ${cond.type === "kind_is" ? "" : "ao-cchip"}`}>
      {body}
      <button type="button" aria-label={`Remove condition “${phrase}”`} onClick={onRemove}>×</button>
    </span>
  );
}

export default function RuleEditor({ open, initial, isNew, folders, watchables, watchablesError, onRetryWatchables, pipelineDefaults, otherRules, onCancel, onSave, onDelete, onShowPreview }) {
  const [rule, setRule] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [serverError, setServerError] = useState(null);
  // "Matches now": {moves} for a document rule, {pages, docs} for a page rule
  const [matches, setMatches] = useState(null);
  const [matchesError, setMatchesError] = useState(false);
  const [previewTry, setPreviewTry] = useState(0);
  // page tags: fetched only while a page tag input is shown; null = not loaded
  const [pageTags, setPageTags] = useState(null);
  const [tagsError, setTagsError] = useState(false);
  const [tagsTry, setTagsTry] = useState(0);
  const [autoName, setAutoName] = useState(true);
  const [renaming, setRenaming] = useState(false);
  const [srcOpen, setSrcOpen] = useState(false);
  const [condOpen, setCondOpen] = useState(false);
  // the index of the condition just added, whose input takes the focus
  const [added, setAdded] = useState(-1);
  const lastPlan = useRef(null);
  // the page and document drafts, kept while the subject is switched back and forth
  const stash = useRef(null);
  const ctx = { folders, watchables };

  useEffect(() => {
    setRule(initial);
    stash.current = null;
    setServerError(null);
    setMatches(null);
    setMatchesError(false);
    setPageTags(null);
    setTagsError(false);
    setRenaming(false);
    setSrcOpen(false);
    setCondOpen(false);
    setAdded(-1);
    // no name = named by its sentence; saving keeps it "" (the list shows the sentence)
    setAutoName(!initial || !initial.name);
  }, [initial]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  const { errors, warnings } = rule ? validate(rule, otherRules, folders) : { errors: [], warnings: [] };
  const page = isPageRule(rule);
  const tagged = !!rule && rule.when.trigger.type === PAGE_TAGGED;
  const needTags = tagged || (!!rule && (rule.conditions || []).some((c) => c.type === "page_tag_is"));

  useEffect(() => {
    if (!open || !needTags || pageTags !== null) return;
    let cancelled = false;
    setTagsError(false);
    apiService.getPageTags()
      .then((res) => !cancelled && setPageTags(res.tags || []))
      .catch(() => !cancelled && setTagsError(true));
    return () => {
      cancelled = true;
    };
  }, [open, needTags, pageTags, tagsTry]);

  // live "Matches now", debounced against preview with the draft rule: a
  // document rule answers a Plan, a page rule a PagePreview. Keyed on what
  // the preview reads: places, trigger and conditions, plus the destination of
  // a document rule (a page rule's prompt and model never change it)
  const previewKey = rule ? JSON.stringify([rule.when, rule.conditions, page ? null : actionOf(rule)]) : "";
  useEffect(() => {
    if (!open || !rule || errors.length) {
      setMatches(null);
      setMatchesError(false);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(() => {
      setMatchesError(false);
      apiService.previewAutomation({ rule: forSave(rule) })
        .then((res) => {
          if (cancelled) return;
          lastPlan.current = res;
          setMatches(res.subject === "page" ? { pages: res.pages, docs: res.docs.length } : { moves: res.moves.length });
        })
        .catch(() => {
          if (cancelled) return;
          setMatches(null);
          setMatchesError(true);
        });
    }, 400);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, previewKey, errors.length, previewTry]);

  if (!rule) return null;

  const update = (patch) => setRule((r) => ({ ...r, ...patch }));
  const updateWhen = (patch) => setRule((r) => ({ ...r, when: { ...r.when, ...patch } }));
  const updateTrigger = (patch) => setRule((r) => ({ ...r, when: { ...r.when, trigger: { ...r.when.trigger, ...patch } } }));
  // one action per rule (R-B2): edits go to actions[0]
  const updateAction = (patch) => setRule((r) => ({ ...r, actions: [{ ...actionOf(r), ...patch }] }));
  const setConditions = (fn) => setRule((r) => ({ ...r, conditions: fn(r.conditions || []) }));
  const t = rule.when.trigger;
  const conds = rule.conditions || [];
  const to = moveTo(rule);
  const cm = to.createIfMissing;
  const key = actionKey(rule);
  const lock = conditionLock(t.type);
  const autoText = sentenceText(rule, ctx);

  const pickTrigger = (type) => {
    const next = chooseTrigger(rule, type, stash.current, { defaults: pipelineDefaults, folders });
    stash.current = next.stash;
    setRule(next.rule);
    setCondOpen(false);
    setAdded(-1);
  };

  const addCondition = (c) => {
    setAdded(conds.length);
    setConditions((cs) => [...cs, c]);
    setCondOpen(false);
  };

  const save = async () => {
    if (errors.length) return;
    setSaving(true);
    setServerError(null);
    try {
      const out = forSave(rule);
      out.name = autoName ? "" : (rule.name || "").trim();
      await onSave(out);
    } catch (e) {
      setServerError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const numInput = (field, min, max) => (
    <input type="number" min={min} max={max} value={t[field]} aria-label={field}
      onChange={(e) => updateTrigger({ [field]: num(e.target.value) })} />
  );
  const tagNames = tagSuggestions(pageTags);
  const src = sourceOf(rule);
  const srcName = src.id === "" ? ROOT_NAME : ((folders || []).find((f) => f.id === src.id) || {}).path || src.name;
  const minAgeFit = conditionFit("min_age_days", rule).ok;

  const tagsStatus = needTags && (tagsError ? (
    <span className="ao-hint bad" role="status">Couldn&apos;t load your page tags{" "}
      <button type="button" className="ao-link" onClick={() => setTagsTry((n) => n + 1)}>(retry)</button></span>
  ) : (
    <span className="ao-hint">{pageTags === null ? "loading your tags…"
      : tagNames.length ? `your tags: ${tagNames.slice(0, 4).join(", ")}${tagNames.length > 4 ? ", …" : ""}` : "no page tags on your pages yet"}</span>
  ));

  const params = (
    <>
      {needTags && <datalist id="ao-page-tags">{tagNames.map((n) => <option key={n} value={n} />)}</datalist>}
      {t.type === PAGE_TAGGED && (
        <div className="ao-frow">
          <span className="ao-lbl">Tag is</span>
          <input type="text" list="ao-page-tags" value={t.tag} placeholder="page tag" aria-label="page tag"
            onChange={(e) => updateTrigger({ tag: e.target.value })} />
          {tagsStatus}
        </div>
      )}
      {t.type === "finished_reading" && (
        <div className="ao-frow">
          at least {numInput("minPages", 1)} pages, not opened for {numInput("settleHours", 0)} hours
          <span className="ao-hint">Inclusive: a {t.minPages}-page document counts.</span>
        </div>
      )}
      {t.type === "read_progress" && (
        <div className="ao-frow">at least {numInput("percent", 1, 100)} % of the way, not opened for {numInput("settleHours", 0)} hours</div>
      )}
      {t.type === "not_opened_for_days" && (
        <div className="ao-frow">
          for {numInput("days", 1)} days
          {minAgeFit && (
            <>
              <button type="button" className="ao-link" onClick={() => addCondition(makeCondition("min_age_days"))}>+ and at least 7 days old</button>
              <span className="ao-hint">Recommended: upload-only documents have never been opened and would match immediately.</span>
            </>
          )}
        </div>
      )}
      {t.type === "not_modified_for_days" && (
        <div className="ao-frow">for {numInput("days", 1)} days <span className="ao-hint">Annotations count as edits. Moves by a rule do not.</span></div>
      )}
      {t.type === "name_matches" && (
        <div className="ao-frow">
          <span className="ao-lbl">Name matches</span>
          <input type="text" value={t.pattern} placeholder="glob or /regex/" aria-label="pattern"
            onChange={(e) => updateTrigger({ pattern: e.target.value })} />
          <span className="ao-hint">case-insensitive · glob (* and ?) or /RE2 regex/</span>
        </div>
      )}
      {t.type === "tag_is" && (
        <div className="ao-frow">
          <span className="ao-lbl">Tag is</span>
          <input type="text" value={t.tag} placeholder="tag name" aria-label="tag"
            onChange={(e) => updateTrigger({ tag: e.target.value })} />
          <span className="ao-hint">document tags from the tablet</span>
        </div>
      )}
      {t.type === "age_since_created" && (
        <div className="ao-frow">at least {numInput("days", 1)} days ago <span className="ao-hint">created time, else last modified, else first seen</span></div>
      )}
      {isTimeType(t.type) && (
        <div className="ao-frow">
          {t.type === "created_time" ? "created" : "changed"}{" "}
          <TimeInputs value={t} label="trigger" onChange={updateTrigger} />
          <span className="ao-hint">
            {t.type === "created_time"
              ? "the tablet's created time; documents without one are skipped"
              : "last edit on the tablet (writing counts); tags added by a rule do not"}
          </span>
        </div>
      )}
      {t.type === "pinned_is" && (
        <div className="ao-frow">
          <label className="chk"><input type="radio" name="ao-pin" checked={t.pinned} onChange={() => updateTrigger({ pinned: true })} /> is pinned</label>
          <label className="chk"><input type="radio" name="ao-pin" checked={!t.pinned} onChange={() => updateTrigger({ pinned: false })} /> is not pinned</label>
          <span className="ao-hint">This rule ignores the default “skip pinned” guard.</span>
        </div>
      )}
    </>
  );

  const places = page ? (
    <>
      <PlacesPicker items={watchables} selected={editableIn(rule)} onChange={(places) => updateWhen({ in: places })} />
      {watchablesError && (
        <div className="ao-hint bad" role="status">Couldn&apos;t load your folders and notebooks{" "}
          <button type="button" className="ao-link" onClick={onRetryWatchables}>(retry)</button></div>
      )}
    </>
  ) : (
    <div className="ao-places">
      <div className="ao-frow">
        <span className="ao-lbl">In</span>
        <button type="button" className="ao-wchip btn" aria-label={`In ${srcName}`} onClick={() => setSrcOpen(!srcOpen)}>
          <span aria-hidden="true">📁</span><span className="n">{srcName}</span>
        </button>
        <button type="button" className="ao-link" aria-expanded={srcOpen} onClick={() => setSrcOpen(!srcOpen)}>{srcOpen ? "done" : "change"}</button>
        <span className="ao-hint">Document rules work on exactly one folder.</span>
      </div>
      {srcOpen && (
        <FolderPicker label="source folder" folders={folders} selected={src.id}
          onSelect={(f) => {
            updateWhen({ in: [{ id: f.id, kind: "folder", name: f.name }] });
            setSrcOpen(false);
          }} />
      )}
    </div>
  );

  // the conditions "+ add a condition" offers now; kind_is offers both kinds
  const offered = conditionsFor(rule.when.subject).filter((type) => conditionFit(type, rule).ok);
  const choices = offered.flatMap((type) => (type === "kind_is"
    ? ["notebooks", "pdf_epub"].map((kind) => ({ key: `kind_is:${kind}`, label: KIND_LABELS[kind], make: () => ({ type, kind }) }))
    : [{ key: type, label: conditionSpec(type).label, make: () => makeCondition(type) }]));
  const conditions = (
    <div className="ao-conds">
      {lock && (
        <div className="ao-frow ao-cond">
          <span className="ao-chip tag">only PDFs &amp; books</span>
          <span className="ao-hint">{lock}</span>
        </div>
      )}
      {conds.map((c, i) => (c.type === "kind_is" && (lock || !conditionPhrase(c)) ? null : (
        <div className="ao-frow ao-cond" key={i}>
          <span className="ao-lbl">{c.type === "kind_is" ? "Only" : "And"}</span>
          <ConditionChip cond={c} index={i} focus={i === added}
            onChange={(patch) => setConditions((cs) => cs.map((x, j) => (j === i ? { ...x, ...patch } : x)))}
            onRemove={() => {
              setAdded(-1);
              setConditions((cs) => cs.filter((_, j) => j !== i));
            }} />
          {c.type === "page_tag_is" && !tagged && tagsStatus}
        </div>
      )))}
      {choices.length > 0 && (condOpen ? (
        <div className="ao-frow ao-cond">
          <span className="ao-lbl">Add</span>
          {choices.map((ch) => (
            <button type="button" key={ch.key} className="ao-btn sm" onClick={() => addCondition(ch.make())}>{ch.label}</button>
          ))}
          <button type="button" className="ao-link" onClick={() => setCondOpen(false)}>cancel</button>
        </div>
      ) : (
        <div className="ao-frow ao-cond">
          <button type="button" className="ao-link" onClick={() => setCondOpen(true)}>+ add a condition</button>
          <span className="ao-hint">{choices.slice(0, 3).map((ch) => ch.label).join(" · ")}{choices.length > 3 ? " · …" : ""}</span>
        </div>
      ))}
    </div>
  );

  const action = actionOf(rule);
  const prompt = (
    <details className="ao-details">
      <summary>Customize what to extract (prompt)</summary>
      <textarea className="ao-prompt" rows={8} value={action.prompt || ""} aria-label="prompt"
        onChange={(e) => updateAction({ prompt: e.target.value })} />
      <div className="ao-prow">
        <button type="button" className="ao-btn link sm" disabled={action.prompt === (pipelineDefaults && pipelineDefaults.prompt)}
          onClick={() => updateAction({ prompt: (pipelineDefaults && pipelineDefaults.prompt) || "" })}>Reset prompt</button>
        <span className="ao-hint">Edits apply to new pages only.</span>
      </div>
      <div className="ao-frow">
        <span className="ao-lbl">Model</span>
        <input type="text" className="ao-namef" value={action.model || ""} aria-label="model"
          onChange={(e) => updateAction({ model: e.target.value })} />
      </div>
    </details>
  );

  // the cards not offered, one callout per reason: [{titles, reason}]
  const notOffered = [];
  for (const a of ACTION_CARDS) {
    const fit = actionFit(a.key, t.type);
    if (fit.ok) continue;
    const same = notOffered.find((n) => n.reason === fit.reason);
    if (same) same.titles.push(a.title);
    else notOffered.push({ titles: [a.title], reason: fit.reason });
  }

  const matchesNow = matchesError ? (
    <span className="ao-hint bad" role="status">Couldn&apos;t load the preview{" "}
      <button type="button" className="ao-link" onClick={() => setPreviewTry((n) => n + 1)}>(retry)</button></span>
  ) : (
    <span className="n">{matches === null ? "…"
      : page ? `${plural(matches.pages, "page")} in ${plural(matches.docs, "document")}` : plural(matches.moves, "document")}</span>
  );
  const show = (
    <button className="ao-btn link sm" disabled={matches === null}
      onClick={() => lastPlan.current && onShowPreview(lastPlan.current, ruleLabel(rule, ctx))}>▸ show</button>
  );
  let preview;
  if (page) {
    // the page-tag list counts at once; the page preview when it cannot (null)
    const tags = pageTagsOf(rule);
    const n = taggedNow(pageTags, rule, matches);
    preview = (
      <>
        <div className="ao-matches">
          Matches now: {matchesNow} {show}
          <span className="ao-hint">Applies from your next change.</span>
        </div>
        {n !== null && (
          <div className="ao-hint">
            {`${plural(n, "page")} ${n === 1 ? "is" : "are"} tagged ${tags.map((x) => `“${x}”`).join(" and ")}${placesOf(rule).length ? " there" : ""} now`}
            {n > 0 && (key === "extract" ? " — saved as notes on the next check; tasks come from pages you tag after saving." : " — saved as notes on the next check.")}
          </div>
        )}
      </>
    );
  } else {
    preview = (
      <div className="ao-matches">
        Matches now: {matchesNow} {show}
        <span className="ao-hint">{isTagAction(action.type) ? tagActionText(action) : `→ ${destinationName(rule, ctx)}`}</span>
      </div>
    );
  }

  return (
    <>
      <div className={`ao-scrim ${open ? "on" : ""}`} onClick={onCancel} />
      <aside className={`ao-drawer ${open ? "on" : ""}`} aria-label="Edit rule" aria-hidden={!open}>
        <div className="ao-dh">
          <h2>{isNew ? "New automation" : "Edit automation"}</h2>
          <button className="x" onClick={onCancel} aria-label="Close">×</button>
        </div>
        <div className="ao-db">
          <div className="ao-step">
            <div className="ao-stepnum i">IF</div>
            <div className="ao-stepb">
              <h3>When this happens…</h3>
              {[["page", "On a page"], ["document", "On a document"]].map(([g, title]) => (
                <div key={g}>
                  <div className="ao-cgrp">{title}</div>
                  <div className="ao-cgrid" role="radiogroup" aria-label={`Trigger: ${title}`}>
                    {TRIGGER_CARDS.filter((c) => c.group === g).map((c) => (
                      <Card key={c.type} card={c} selected={t.type === c.type} onPick={() => pickTrigger(c.type)}
                        badge={c.pdfOnly ? PDF_ONLY_BADGE : null} />
                    ))}
                  </div>
                </div>
              ))}
              <div className="ao-fill">
                {params}
                {places}
                {conditions}
                <div className="ao-fixed">
                  <span className="ao-chip tag">{NO_AUTOMATION_TAG}</span> Documents carrying this tag are always skipped, by every rule.
                </div>
              </div>
            </div>
          </div>

          <div className="ao-vline" />

          <div className="ao-step">
            <div className="ao-stepnum t">THEN</div>
            <div className="ao-stepb">
              <h3>…do this</h3>
              <div className="ao-cgrid" role="radiogroup" aria-label="Action">
                {ACTION_CARDS.map((a) => (
                  <Card key={a.key} card={a} selected={key === a.key} disabled={!actionFit(a.key, t.type).ok}
                    onPick={() => setRule(chooseAction(rule, a.key))} />
                ))}
              </div>
              {notOffered.map((n) => (
                <div className="ao-callout" key={n.reason}>
                  <b>{n.titles.join(", ")}</b> {n.titles.length === 1 ? "is" : "are"} not offered here. {n.reason}
                </div>
              ))}
              <div className="ao-fill">
                {key === "move" && (
                  <>
                    <div className="ao-frow"><span className="ao-lbl">Move it to</span><b>{destinationName(rule, ctx)}</b></div>
                    <FolderPicker label="destination folder" folders={folders} selected={cm ? null : to.id}
                      onSelect={(f) => updateAction({ to: { id: f.id, name: f.name } })}
                      extra={
                        <li className={`new ${cm ? "sel" : ""}`} onClick={() => updateAction({ to: { id: "", name: "", createIfMissing: cm || { name: "", parent: "source" } } })}>
                          + New subfolder of source
                        </li>
                      } />
                    {cm && (
                      <div className="ao-newname">
                        <span>Named</span>
                        <input type="text" placeholder="e.g. Read" value={cm.name} aria-label="new folder name" autoFocus
                          onChange={(e) => updateAction({ to: { id: "", name: "", createIfMissing: { ...cm, name: e.target.value } } })} />
                        <span className="ao-hint">Created on the first live run if no folder of that name exists under {srcName}.</span>
                      </div>
                    )}
                  </>
                )}
                {isTagAction(action.type) && (
                  <div className="ao-frow">
                    <span className="ao-lbl">Tag</span>
                    <input type="text" value={action.tag || ""} placeholder="e.g. new" aria-label="tag to change"
                      onChange={(e) => updateAction({ tag: e.target.value })} />
                    <span className="ao-hint">
                      {action.type === "add_tag" ? "Added on the tablet when the rule matches." : "Taken off on the tablet when the rule matches."}
                      {" "}Document tag; case, spaces, - and _ don&apos;t matter.
                    </span>
                  </div>
                )}
                {key === "extract" && (
                  <div className="ao-frow"><span className="ao-hint">Skips anything marked -inbox. Tasks land in inbox-remarkable.md, notes in personal/handwritten/.</span></div>
                )}
                {key === "notes" && (
                  <div className="ao-frow"><span className="ao-hint">Transcribes each page into its note in personal/handwritten/. No tasks are extracted.</span></div>
                )}
                {isPageKey(key) && prompt}
              </div>
            </div>
          </div>

          <div className="ao-summary" aria-label="Summary">
            <div className="ao-hint">This rule reads:</div>
            <Sentence rule={rule} ctx={ctx} />
            {preview}
          </div>

          {errors.map((e) => <div className="ao-valid" key={e}>⚠ <span>{e}</span></div>)}
          {warnings.map((w) => <div className="ao-valid warn" key={w}>⚠ <span>{w}</span></div>)}
          {serverError && <div className="ao-valid bad" role="alert">⚠ <span>{serverError}</span></div>}
        </div>

        <div className="ao-df">
          <div className="ao-namerow">
            {renaming ? (
              <input type="text" className="ao-namef" value={autoName ? "" : rule.name} placeholder={autoText} autoFocus
                aria-label="rule name"
                onChange={(e) => {
                  setAutoName(e.target.value.trim() === "");
                  update({ name: e.target.value });
                }} />
            ) : (
              <span className="ao-hint">
                Name: {autoName ? "auto from the sentence" : <b>{rule.name}</b>} ·{" "}
                <button type="button" className="ao-link" onClick={() => setRenaming(true)}>rename</button>
              </span>
            )}
          </div>
          <div className="ao-dfb">
            {!isNew && <button className="ao-btn danger" onClick={() => onDelete(rule)}>Delete</button>}
            <span className="ao-spacer" />
            <button className="ao-btn" onClick={onCancel}>Cancel</button>
            <button className="ao-btn primary" onClick={save} disabled={saving || errors.length > 0}>Save</button>
          </div>
        </div>
      </aside>
    </>
  );
}

function isPageKey(key) {
  return key === "extract" || key === "notes";
}
