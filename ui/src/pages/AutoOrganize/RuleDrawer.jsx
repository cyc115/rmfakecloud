import { useEffect, useRef, useState } from "react";
import apiService from "../../services/api.service";
import FolderPicker from "./FolderPicker";
import {
  LATER_TRIGGERS, NO_AUTOMATION_TAG, TRIGGERS,
  destinationLabel, isPageBased, makeTrigger, patternError, ruleSentence, sourceLabel,
} from "./rules";

function num(v) {
  const n = parseInt(v, 10);
  return Number.isNaN(n) ? 0 : n;
}

// Client side checks shown inline; the server validates again on save.
function validate(rule, otherRules) {
  const errors = [];
  const warnings = [];
  const cm = rule.destination.createIfMissing;
  if (cm) {
    if (!cm.name.trim()) errors.push("Name the new subfolder.");
  } else if (rule.destination.id === rule.source.id) {
    errors.push("Destination is the same as the source. Pick a different folder.");
  }
  const t = rule.trigger;
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

export default function RuleDrawer({ open, initial, isNew, folders, otherRules, onCancel, onSave, onDelete, onShowPreview }) {
  const [rule, setRule] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [serverError, setServerError] = useState(null);
  const [matches, setMatches] = useState(null);
  const lastPlan = useRef(null);

  useEffect(() => {
    setRule(initial);
    setServerError(null);
    setMatches(null);
  }, [initial]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  const { errors, warnings } = rule ? validate(rule, otherRules) : { errors: [], warnings: [] };

  // live "Matches now", debounced against preview with the draft rule
  useEffect(() => {
    if (!open || !rule || errors.length) {
      setMatches(null);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(() => {
      apiService.previewAutoOrganize({ rule })
        .then((plan) => {
          if (cancelled) return;
          lastPlan.current = plan;
          setMatches(plan.moves.length);
        })
        .catch(() => !cancelled && setMatches(null));
    }, 400);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, JSON.stringify(rule), errors.length]);

  if (!rule) return null;

  const update = (patch) => setRule((r) => ({ ...r, ...patch }));
  const updateTrigger = (patch) => setRule((r) => ({ ...r, trigger: { ...r.trigger, ...patch } }));
  const cm = rule.destination.createIfMissing;
  const locked = isPageBased(rule.trigger.type);

  const setTriggerType = (type) => {
    setRule((r) => ({ ...r, trigger: makeTrigger(type), scope: isPageBased(type) ? "pdf_epub" : r.scope }));
  };

  const save = async () => {
    if (errors.length) return;
    setSaving(true);
    setServerError(null);
    try {
      await onSave(rule);
    } catch (e) {
      setServerError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const t = rule.trigger;
  const numInput = (field, min, max) => (
    <input type="number" min={min} max={max} value={t[field]} aria-label={field}
      onChange={(e) => updateTrigger({ [field]: num(e.target.value) })} />
  );

  return (
    <>
      <div className={`ao-scrim ${open ? "on" : ""}`} onClick={onCancel} />
      <aside className={`ao-drawer ${open ? "on" : ""}`} aria-label="Edit rule" aria-hidden={!open}>
        <div className="ao-dh">
          <h2>{isNew ? "New rule" : "Edit rule"}</h2>
          <button className="x" onClick={onCancel} aria-label="Close">×</button>
        </div>
        <div className="ao-db">
          <div className="ao-ph">When a document in</div>
          <FolderPicker label="source folder" folders={folders} selected={rule.source.id}
            onSelect={(f) => update({ source: { id: f.id, name: f.name } })} />

          <div className="ao-ph">that applies to</div>
          <div className="ao-radios">
            {[["pdf_epub", "PDF & EPUB"], ["notebooks", "Notebooks"], ["all", "Both"]].map(([v, l]) => (
              <label key={v}>
                <input type="radio" name="ao-scope" value={v} checked={rule.scope === v}
                  disabled={locked && v !== "pdf_epub"} onChange={() => update({ scope: v })} /> {l}
              </label>
            ))}
          </div>
          <div className="ao-fixed">
            <span className="ao-chip tag">{NO_AUTOMATION_TAG}</span> Documents carrying this tag are always skipped, by every rule.
          </div>

          <div className="ao-ph">and</div>
          <select className="ao-trig" value={t.type} onChange={(e) => setTriggerType(e.target.value)} aria-label="Trigger">
            {TRIGGERS.map((o) => <option key={o.type} value={o.type}>{o.label}</option>)}
            {Object.entries(LATER_TRIGGERS).map(([stage, list]) => [
              <option key={stage} disabled>── {stage} ──</option>,
              ...list.map((l) => <option key={l} disabled>{l}</option>),
            ])}
          </select>

          <div className="ao-frag">
            {t.type === "finished_reading" && (
              <>
                and has at least {numInput("minPages", 1)} pages, and hasn't been opened for {numInput("settleHours", 0)} hours
                <br /><span className="ao-hint">Inclusive: a {t.minPages}-page document counts. Locked to PDF &amp; EPUB. Both lastOpenedPage values are logged in Preview.</span>
              </>
            )}
            {t.type === "read_progress" && (
              <>at least {numInput("percent", 1, 100)} % of the way, and hasn't been opened for {numInput("settleHours", 0)} hours</>
            )}
            {t.type === "not_opened_for_days" && (
              <>
                for {numInput("days", 1)} days &nbsp;
                <label className="chk">
                  <input type="checkbox" checked={t.minAgeEnabled} onChange={(e) => updateTrigger({ minAgeEnabled: e.target.checked })} />
                  and is at least {numInput("minAgeDays", 0)} days old
                </label>
                <br /><span className="ao-hint">Recommended on: upload-only documents have never been opened and would match immediately.</span>
              </>
            )}
            {t.type === "not_modified_for_days" && (
              <>for {numInput("days", 1)} days<br /><span className="ao-hint">Annotations count as modifications. Sweeper moves do not reset this clock.</span></>
            )}
            {t.type === "name_matches" && (
              <>
                matching <input type="text" value={t.pattern} placeholder="glob or /regex/" aria-label="pattern"
                  onChange={(e) => updateTrigger({ pattern: e.target.value })} />{" "}
                <span className="ao-hint">case-insensitive · glob (* and ?) or /RE2 regex/</span>
              </>
            )}
            {t.type === "tag_is" && (
              <>
                tagged <input type="text" value={t.tag} placeholder="tag name" aria-label="tag"
                  onChange={(e) => updateTrigger({ tag: e.target.value })} />{" "}
                <span className="ao-hint">document-level tags from the tablet</span>
              </>
            )}
            {t.type === "age_since_created" && (
              <>at least {numInput("days", 1)} days ago <span className="ao-hint">createdTime, else lastModified, else first seen</span></>
            )}
            {t.type === "pinned_is" && (
              <>
                <label className="chk"><input type="radio" name="ao-pin" checked={t.pinned} onChange={() => updateTrigger({ pinned: true })} /> is pinned</label>
                &nbsp;
                <label className="chk"><input type="radio" name="ao-pin" checked={!t.pinned} onChange={() => updateTrigger({ pinned: false })} /> is not pinned</label>
                <br /><span className="ao-hint">This rule ignores the default “skip pinned” guard.</span>
              </>
            )}
          </div>

          <div className="ao-ph">move it to</div>
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
              <span className="ao-hint">Created on the first live sweep if no folder of that name exists under {sourceLabel(rule, folders)}.</span>
            </div>
          )}

          <div className="ao-ph">Action</div>
          <div className="ao-act">Move <span className="ao-hint">· other actions (notify digest, tag, pin, rename, trash) arrive in later versions</span></div>

          <div className="ao-ph">Name <span className="ao-hint">(optional)</span></div>
          <input type="text" className="ao-namef" value={rule.name} placeholder={ruleSentence(rule, folders)}
            onChange={(e) => update({ name: e.target.value })} aria-label="rule name" />

          {errors.map((e) => <div className="ao-valid" key={e}>⚠ <span>{e}</span></div>)}
          {warnings.map((w) => <div className="ao-valid warn" key={w}>⚠ <span>{w}</span></div>)}
          {serverError && <div className="ao-valid bad">⚠ <span>{serverError}</span></div>}
        </div>

        <div className="ao-df">
          <div className="ao-matches">
            Matches now:{" "}
            <span className="n">{matches === null ? "…" : `${matches} document${matches === 1 ? "" : "s"}`}</span>
            <button className="ao-btn link sm" disabled={matches === null}
              onClick={() => lastPlan.current && onShowPreview(lastPlan.current)}>▸ show</button>
            <span className="ao-hint">→ {destinationLabel(rule, folders)}</span>
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
