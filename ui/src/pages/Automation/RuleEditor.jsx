import { useEffect, useRef, useState } from "react";
import apiService from "../../services/api.service";
import FolderPicker from "./FolderPicker";
import PlacesPicker from "./PlacesPicker";
import Sentence from "./Sentence";
import {
  ACTION_CARDS, CONDITIONS, NO_AUTOMATION_TAG, PAGE_TAGGED, PDF_ONLY_BADGE, ROOT_NAME, TRIGGER_CARDS,
  actionFit, actionKey, chooseAction, chooseTrigger, conditionLock, destinationName, forSave, isPageTrigger,
  editableWatch, normalizePageTag, patternError, placesOf, sentenceText, tagSuggestions, taggedCount,
} from "./rules";

function num(v) {
  const n = parseInt(v, 10);
  return Number.isNaN(n) ? 0 : n;
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

// Client side checks shown inline; the server validates again on save.
function validate(rule, otherRules, folders) {
  const errors = [];
  const warnings = [];
  const t = rule.trigger;
  if (isPageTrigger(t.type)) {
    if (t.type === PAGE_TAGGED && !normalizePageTag(t.tag)) errors.push("Pick or type a page tag.");
    return { errors, warnings };
  }
  const cm = rule.destination.createIfMissing;
  // the pickers list only folders outside the Trash
  const exists = (id) => (folders || []).some((f) => f.id === id);
  if (!exists(rule.source.id)) errors.push("The folder in “In” is missing or in the Trash. Pick a folder.");
  if (cm) {
    if (!cm.name.trim()) errors.push("Name the new subfolder.");
  } else if (!exists(rule.destination.id)) {
    errors.push("Destination folder missing or in Trash. Pick a folder.");
  } else if (rule.destination.id === rule.source.id) {
    errors.push("Destination is the same as the source. Pick a different folder.");
  }
  if (t.type === "name_matches") {
    const e = patternError(t.pattern);
    if (e) errors.push(e);
  }
  if (t.type === "tag_is" && !t.tag.trim()) errors.push("Enter a tag.");
  if (t.type === "tag_is" && t.tag.trim().toLowerCase().replace(/[\s_-]+/g, " ") === NO_AUTOMATION_TAG) {
    errors.push("“no automation” documents are always skipped; pick another tag.");
  }
  if (t.type === "finished_reading" && t.minPages < 1) errors.push("Minimum pages must be at least 1.");
  if (t.type === "read_progress" && (t.percent < 1 || t.percent > 100)) errors.push("Percent must be between 1 and 100.");
  if (["not_opened_for_days", "not_modified_for_days", "age_since_created"].includes(t.type) && t.days < 1) {
    errors.push("Days must be at least 1.");
  }
  if (!cm) {
    const loops = otherRules.filter((o) => o.enabled && o.source.id === rule.destination.id &&
      !o.destination.createIfMissing && o.destination.id === rule.source.id);
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

export default function RuleEditor({ open, initial, isNew, folders, watchables, watchablesError, onRetryWatchables, pipelineDefaults, otherRules, onCancel, onSave, onDelete, onShowPreview }) {
  const [rule, setRule] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [serverError, setServerError] = useState(null);
  const [matches, setMatches] = useState(null);
  const [matchesError, setMatchesError] = useState(false);
  const [previewTry, setPreviewTry] = useState(0);
  // page tags: fetched only while "I tag a page" is picked; null = not loaded
  const [pageTags, setPageTags] = useState(null);
  const [tagsError, setTagsError] = useState(false);
  const [tagsTry, setTagsTry] = useState(0);
  const [autoName, setAutoName] = useState(true);
  const [renaming, setRenaming] = useState(false);
  const [srcOpen, setSrcOpen] = useState(false);
  const [condOpen, setCondOpen] = useState(false);
  const lastPlan = useRef(null);
  // the page and document drafts, kept while the trigger group is switched back and forth
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
  const page = rule ? isPageTrigger(rule.trigger.type) : false;
  const tagged = !!rule && rule.trigger.type === PAGE_TAGGED;

  useEffect(() => {
    if (!open || !tagged || pageTags !== null) return;
    let cancelled = false;
    setTagsError(false);
    apiService.getPageTags()
      .then((res) => !cancelled && setPageTags(res.tags || []))
      .catch(() => !cancelled && setTagsError(true));
    return () => {
      cancelled = true;
    };
  }, [open, tagged, pageTags, tagsTry]);

  // live "Matches now" for document rules, debounced against preview with the draft rule
  useEffect(() => {
    if (!open || !rule || errors.length || page) {
      setMatches(null);
      setMatchesError(false);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(() => {
      setMatchesError(false);
      apiService.previewAutoOrganize({ rule })
        .then((plan) => {
          if (cancelled) return;
          lastPlan.current = plan;
          setMatches(plan.moves.length);
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
  }, [open, JSON.stringify(rule), errors.length, previewTry]);

  if (!rule) return null;

  const update = (patch) => setRule((r) => ({ ...r, ...patch }));
  const updateTrigger = (patch) => setRule((r) => ({ ...r, trigger: { ...r.trigger, ...patch } }));
  const updateAction = (patch) => setRule((r) => ({ ...r, action: { ...r.action, ...patch } }));
  const t = rule.trigger;
  const cm = rule.destination.createIfMissing;
  const key = actionKey(rule);
  const lock = conditionLock(t.type);
  const autoText = sentenceText(rule, ctx);

  const pickTrigger = (type) => {
    const next = chooseTrigger(rule, type, stash.current, { defaults: pipelineDefaults, folders });
    stash.current = next.stash;
    setRule(next.rule);
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
  const srcName = rule.source.id === "" ? ROOT_NAME : ((folders || []).find((f) => f.id === rule.source.id) || {}).path || rule.source.name;

  const params = (
    <>
      {t.type === PAGE_TAGGED && (
        <div className="ao-frow">
          <span className="ao-lbl">Tag is</span>
          <input type="text" list="ao-page-tags" value={t.tag} placeholder="page tag" aria-label="page tag"
            onChange={(e) => updateTrigger({ tag: e.target.value })} />
          <datalist id="ao-page-tags">{tagNames.map((n) => <option key={n} value={n} />)}</datalist>
          {tagsError ? (
            <span className="ao-hint bad" role="status">Couldn&apos;t load your page tags{" "}
              <button type="button" className="ao-link" onClick={() => setTagsTry((n) => n + 1)}>(retry)</button></span>
          ) : (
            <span className="ao-hint">{pageTags === null ? "loading your tags…"
              : tagNames.length ? `your tags: ${tagNames.slice(0, 4).join(", ")}${tagNames.length > 4 ? ", …" : ""}` : "no page tags on your pages yet"}</span>
          )}
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
          <label className="chk">
            <input type="checkbox" checked={t.minAgeEnabled} onChange={(e) => updateTrigger({ minAgeEnabled: e.target.checked })} />
            and is at least {numInput("minAgeDays", 0)} days old
          </label>
          <span className="ao-hint">Recommended on: upload-only documents have never been opened and would match immediately.</span>
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
      <PlacesPicker items={watchables} selected={editableWatch(rule)} onChange={(watch) => update({ watch })} />
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
        <FolderPicker label="source folder" folders={folders} selected={rule.source.id}
          onSelect={(f) => {
            update({ source: { id: f.id, name: f.name } });
            setSrcOpen(false);
          }} />
      )}
    </div>
  );

  const cond = CONDITIONS.find((c) => c.scope === rule.scope);
  const condition = (
    <div className="ao-frow ao-cond">
      {lock ? (
        <>
          <span className="ao-chip tag">only PDFs &amp; books</span>
          <span className="ao-hint">{lock}</span>
        </>
      ) : cond ? (
        <>
          <span className="ao-lbl">Only</span>
          <span className="ao-wchip">
            <span className="n">{cond.label.replace(/^only /, "")}</span>
            <button type="button" aria-label="Remove condition" onClick={() => update({ scope: "all" })}>×</button>
          </span>
        </>
      ) : condOpen ? (
        <>
          <span className="ao-lbl">Add</span>
          {CONDITIONS.map((c) => (
            <button type="button" key={c.scope} className="ao-btn sm" onClick={() => {
              update({ scope: c.scope });
              setCondOpen(false);
            }}>{c.label}</button>
          ))}
          <button type="button" className="ao-link" onClick={() => setCondOpen(false)}>cancel</button>
        </>
      ) : (
        <>
          <button type="button" className="ao-link" onClick={() => setCondOpen(true)}>+ add a condition</button>
          <span className="ao-hint">only notebooks · only PDFs &amp; books</span>
        </>
      )}
    </div>
  );

  const prompt = (
    <details className="ao-details">
      <summary>Customize what to extract (prompt)</summary>
      <textarea className="ao-prompt" rows={8} value={rule.action.prompt || ""} aria-label="prompt"
        onChange={(e) => updateAction({ prompt: e.target.value })} />
      <div className="ao-prow">
        <button type="button" className="ao-btn link sm" disabled={rule.action.prompt === (pipelineDefaults && pipelineDefaults.prompt)}
          onClick={() => updateAction({ prompt: (pipelineDefaults && pipelineDefaults.prompt) || "" })}>Reset prompt</button>
        <span className="ao-hint">Edits apply to new pages only.</span>
      </div>
      <div className="ao-frow">
        <span className="ao-lbl">Model</span>
        <input type="text" className="ao-namef" value={rule.action.model || ""} aria-label="model"
          onChange={(e) => updateAction({ model: e.target.value })} />
      </div>
    </details>
  );

  const disabledActions = ACTION_CARDS.filter((a) => !actionFit(a.key, t.type).ok);

  let preview;
  if (page) {
    const n = tagged && normalizePageTag(t.tag) && pageTags !== null ? taggedCount(pageTags, rule) : null;
    preview = (
      <div className="ao-matches">
        <span className="n">Applies from your next change</span>
        {n !== null && (
          <span className="ao-hint">
            {`${plural(n, "page")} ${n === 1 ? "is" : "are"} tagged “${normalizePageTag(t.tag)}” ${placesOf(rule).length ? "there" : ""} now`.replace("  ", " ")}
            {n > 0 && (key === "extract" ? " — saved as notes on the next check; tasks come from pages you tag after saving." : " — saved as notes on the next check.")}
          </span>
        )}
      </div>
    );
  } else {
    preview = (
      <div className="ao-matches">
        Matches now:{" "}
        {matchesError ? (
          <span className="ao-hint bad" role="status">Couldn&apos;t load the preview{" "}
            <button type="button" className="ao-link" onClick={() => setPreviewTry((n) => n + 1)}>(retry)</button></span>
        ) : (
          <span className="n">{matches === null ? "…" : plural(matches, "document")}</span>
        )}
        <button className="ao-btn link sm" disabled={matches === null}
          onClick={() => lastPlan.current && onShowPreview(lastPlan.current)}>▸ show</button>
        <span className="ao-hint">→ {destinationName(rule, ctx)}</span>
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
                {condition}
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
              {disabledActions.map((a) => (
                <div className="ao-callout" key={a.key}><b>{a.title}</b> is not offered here. {actionFit(a.key, t.type).reason}</div>
              ))}
              <div className="ao-fill">
                {key === "move" && (
                  <>
                    <div className="ao-frow"><span className="ao-lbl">Move it to</span><b>{destinationName(rule, ctx)}</b></div>
                    <FolderPicker label="destination folder" folders={folders} selected={cm ? null : rule.destination.id}
                      onSelect={(f) => update({ destination: { id: f.id, name: f.name } })}
                      extra={
                        <li className={`new ${cm ? "sel" : ""}`} onClick={() => update({ destination: { id: "", name: "", createIfMissing: cm || { name: "", parent: "source" } } })}>
                          + New subfolder of source
                        </li>
                      } />
                    {cm && (
                      <div className="ao-newname">
                        <span>Named</span>
                        <input type="text" placeholder="e.g. Read" value={cm.name} aria-label="new folder name" autoFocus
                          onChange={(e) => update({ destination: { id: "", name: "", createIfMissing: { ...cm, name: e.target.value } } })} />
                        <span className="ao-hint">Created on the first live run if no folder of that name exists under {srcName}.</span>
                      </div>
                    )}
                  </>
                )}
                {key === "extract" && (
                  <div className="ao-frow"><span className="ao-hint">Skips anything marked -inbox. Tasks land in inbox-remarkable.md, notes in personal/handwritten/.</span></div>
                )}
                {key === "notes" && (
                  <div className="ao-frow"><span className="ao-hint">Transcribes each page into its note in personal/handwritten/. No tasks are extracted.</span></div>
                )}
                {isPipelineKey(key) && prompt}
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

function isPipelineKey(key) {
  return key === "extract" || key === "notes";
}
