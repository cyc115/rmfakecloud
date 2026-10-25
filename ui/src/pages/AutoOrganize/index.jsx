import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "react-toastify";
import apiService from "../../services/api.service";
import Spinner from "../../components/Spinner";
import RuleDrawer from "./RuleDrawer";
import {
  INTERVALS, SKIP_CHIP, SKIP_LABELS, destinationLabel, exampleRule, fmtAgo, fmtTime,
  newRule, sourceLabel, triggerPhrase, withDisplayNames,
} from "./rules";
import "./AutoOrganize.scss";

function statusText(settings, status) {
  if (status.running) return { cls: "running", text: "Sweep running…" };
  const last = status.lastSweep ? `last sweep ${fmtTime(status.lastSweep.at)}` : "no sweep yet";
  if (!settings.enabled) return { cls: "paused", text: `Paused · ${last}` };
  const next = status.nextAt ? ` · next ${fmtTime(status.nextAt)}` : "";
  return { cls: "", text: `Running every ${settings.intervalMinutes} min · ${last}${next}` };
}

function Kebab({ items }) {
  const ref = useRef(null);
  useEffect(() => {
    const close = (e) => {
      if (ref.current && ref.current.open && !ref.current.contains(e.target)) ref.current.removeAttribute("open");
    };
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, []);
  return (
    <details className="ao-kebab" ref={ref}>
      <summary aria-label="Rule actions">⋯</summary>
      <div className="ao-menu">
        {items.map((it, i) => it === "-" ? <hr key={i} /> : (
          <button key={it.label} className={it.danger ? "del" : ""} disabled={it.disabled}
            onClick={() => {
              ref.current.removeAttribute("open");
              it.onClick();
            }}>{it.label}</button>
        ))}
      </div>
    </details>
  );
}

function RuleRow({ rule, index, count, view, folders, sharesSource, onToggle, onEdit, onDuplicate, onPreview, onMove, onDelete, drag }) {
  const chips = [];
  if (view) {
    for (const p of view.problems) chips.push(<span key={p} className="ao-chip bad">{p}</span>);
    if (rule.enabled && view.problems.length === 0) {
      chips.push(<span key="m" className={`ao-chip ${view.matches > 0 ? "ok" : ""}`}>{view.matches} match now</span>);
    }
    chips.push(<span key="l" className="ao-chip">{view.lastMoveAt ? `last move ${fmtAgo(view.lastMoveAt)}` : "never moved"}</span>);
    if (view.holds > 0) chips.push(<span key="h" className="ao-chip">{view.holds} held</span>);
    if (view.optedOut > 0) chips.push(<span key="o" className="ao-chip tag">{view.optedOut} opted out</span>);
    if (view.destWillCreate) chips.push(<span key="c" className="ao-chip">folder created on first live move</span>);
    if (view.loopsWith && view.loopsWith.length) {
      chips.push(<span key="lw" className="ao-chip warn" title="The 24 h cycle guard limits this to one round trip.">loops with rule {view.loopsWith.join(", ")}</span>);
    }
  }
  if (sharesSource) chips.push(<span key="om" className="ao-chip order" title="Shares a source with another rule; the first match wins">order matters</span>);
  if (!rule.enabled) chips.push(<span key="d" className="ao-chip">disabled</span>);

  const src = sourceLabel(rule, folders, view && view.sourceName);
  const dst = destinationLabel(rule, folders, view && view.destName);
  return (
    <div className={`ao-rule ${rule.enabled ? "" : "off"} ${drag.over === index ? "dragover" : ""}`}
      onDragOver={(e) => { e.preventDefault(); drag.setOver(index); }}
      onDragLeave={() => drag.setOver(null)}
      onDrop={(e) => { e.preventDefault(); drag.drop(index); }}>
      <span className="ao-grip" title="Drag to reorder" draggable
        onDragStart={(e) => { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", String(index)); drag.start(index); }}
        onDragEnd={() => drag.setOver(null)}>⋮⋮</span>
      <label className="ao-sw" title={rule.enabled ? "Enabled" : "Disabled"}>
        <input type="checkbox" checked={rule.enabled} onChange={(e) => onToggle(e.target.checked)} aria-label={`Rule ${index + 1} enabled`} />
        <i />
      </label>
      <div className="ao-sent">
        <div className="s">
          <span className="ao-num">{index + 1}.</span>
          {rule.name ? <><b>{rule.name}</b><span className="ao-hint"> · </span></> : null}
          <b>{src}</b><span className="arr">→</span><span className="p">{triggerPhrase(rule.trigger)}</span>
          <span className="arr">→</span><b>{dst}</b>
          {view && view.sourceRenamedFrom && <span className="ao-hint"> (source renamed from “{view.sourceRenamedFrom}”)</span>}
          {view && view.destRenamedFrom && <span className="ao-hint"> (renamed from “{view.destRenamedFrom}”)</span>}
        </div>
        <div className="ao-chips">{chips}</div>
      </div>
      <Kebab items={[
        { label: "Edit", onClick: onEdit },
        { label: "Duplicate", onClick: onDuplicate },
        { label: "Preview this rule", onClick: onPreview },
        "-",
        { label: "Move up", onClick: () => onMove(-1), disabled: index === 0 },
        { label: "Move down", onClick: () => onMove(1), disabled: index === count - 1 },
        "-",
        { label: "Delete", onClick: onDelete, danger: true },
      ]} />
    </div>
  );
}

function PreviewPane({ plan, live, onApply, onReapply, busy }) {
  if (!plan) {
    return <div className="ao-empty-pane">Press <b>Preview</b> to see what the next sweep would do. Nothing moves.</div>;
  }
  const skipped = plan.skipped.filter((s) => s.reason !== "claimed");
  const claimed = plan.skipped.filter((s) => s.reason === "claimed");
  const groups = plan.rules.filter((r) => r.enabled || plan.rules.length === 1);
  return (
    <>
      {groups.map((r) => {
        const moves = plan.moves.filter((m) => m.ruleId === r.ruleId);
        const label = r.name || `${r.sourceName} → ${r.destName}`;
        let hint = moves.length ? `${moves.length} would move` : "nothing qualifies";
        if (r.problems.length) hint = r.problems.join(" · ");
        return (
          <div key={r.ruleId}>
            <div className="ao-grp"><b>{r.number}. {label}</b><span className={`ao-hint ${r.problems.length ? "bad" : ""}`}>{hint}</span></div>
            {moves.map((m) => (
              <div className="ao-row" key={m.docId}>
                <div><div className="doc">{m.docName}</div><div className="d">{m.detail}</div></div>
                <div className="to"><span className="arr">→</span>{m.toName}{m.createFolder ? " (new folder)" : ""}</div>
              </div>
            ))}
          </div>
        );
      })}
      {groups.length === 0 && <div className="ao-empty-pane">No enabled rules.</div>}
      <details className="ao-skipped">
        <summary>Skipped ({skipped.length}){claimed.length ? ` · ${claimed.length} claimed by an earlier rule` : ""}</summary>
        {[...skipped, ...claimed].map((s) => (
          <div className="ao-skip" key={`${s.ruleId}-${s.docId}`}>
            <div><b>{s.docName}</b> <span className="why">{s.folder} · rule {s.ruleNumber} · {s.detail}</span></div>
            <span className="ao-skip-r">
              <span className={`ao-chip ${SKIP_CHIP[s.reason] || ""}`}>{SKIP_LABELS[s.reason] || s.reason}</span>
              {s.reason === "held" && s.holdId && (
                <button className="ao-btn sm" disabled={busy} onClick={() => onReapply(s.holdId)}>Re-apply</button>
              )}
            </span>
          </div>
        ))}
        {skipped.length + claimed.length === 0 && <div className="ao-skip"><span className="why">Nothing skipped.</span></div>}
      </details>
      <div className="ao-apply">
        <button className="ao-btn primary" disabled={!live || plan.moves.length === 0 || busy} onClick={onApply}>
          Apply {plan.moves.length === 1 ? "this move" : `these ${plan.moves.length} moves`}
        </button>
        <span className="ao-hint">
          {live ? `Live: this will move ${plan.moves.length} document${plan.moves.length === 1 ? "" : "s"}. You will be asked to confirm.`
            : "Switch to Live to apply. In Dry-run the preview is informational."}
        </span>
      </div>
    </>
  );
}

function MovesPane({ moves, onUndo, onReapply, busy }) {
  if (!moves.length) return <div className="ao-empty-pane">No sweeper moves yet.</div>;
  return (
    <>
      {moves.map((m) => (
        <div className="ao-mv" key={m.id}>
          <span className="when">{fmtTime(m.at)}</span>
          <div className="what">
            <div className="doc">{m.docName}</div>
            <div className="path">{m.fromName}<span className="arr">→</span>{m.toName}{" "}
              <span className="rule">· rule {m.ruleNumber} {m.ruleName}
                {m.kind === "reapply" ? " · re-applied" : ""}
                {m.undoneAt ? ` · undone ${fmtTime(m.undoneAt)} · hold set` : ""}
                {m.holdId ? " · returned to source, held" : ""}
                {m.detail && m.kind === "rule" ? ` · ${m.detail}` : ""}</span>
            </div>
          </div>
          <span className={`ao-chip ${m.mode === "LIVE" ? "live" : "dry"}`}>{m.mode}</span>
          <span className="ao-mv-act">
            {m.holdId && <button className="ao-btn sm" disabled={busy} onClick={() => onReapply(m.holdId)}>Re-apply</button>}
            {!m.holdId && m.canUndo && <button className="ao-btn sm" disabled={busy} onClick={() => onUndo(m)}>Undo</button>}
            {m.undoneAt ? <span className="ao-chip">undone</span> : null}
          </span>
        </div>
      ))}
      <div className="ao-grp"><span className="ao-hint">Last 50 sweeper moves. Manual moves from the tablet are not listed. Undo reverses the move and sets a hold so the rule does not re-move it while its trigger inputs are unchanged.</span></div>
    </>
  );
}

function RunsPane({ runs }) {
  if (!runs.length) return <div className="ao-empty-pane">No sweeps yet.</div>;
  return (
    <div className="ao-runs-wrap">
      <table className="ao-runs">
        <thead>
          <tr><th>Time</th><th>Mode</th><th>Scanned</th><th>Moved</th><th className="hide-m">Skipped</th><th>Errors</th><th className="hide-m">Duration</th><th className="hide-m">Trigger</th></tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id}>
              <td>{fmtTime(r.at)}</td>
              <td><span className={`ao-chip ${r.mode === "LIVE" ? "live" : "dry"}`}>{r.mode}</span></td>
              <td>{r.scanned}</td>
              <td title={r.mode === "DRY" ? "would move (dry-run moves are never counted as moves)" : ""}>
                {r.mode === "LIVE" ? r.moved : <span className="ao-hint">{r.planned} would</span>}
              </td>
              <td className="hide-m">{r.skipped}</td>
              <td>
                {r.errorDetails && r.errorDetails.length ? (
                  <details><summary className={r.errors ? "err" : ""}>{r.errors} ▸</summary><span className="errbox">{r.errorDetails.join(" · ")}</span></details>
                ) : r.errors}
              </td>
              <td className="hide-m">{(r.durationMs / 1000).toFixed(1)} s</td>
              <td className="hide-m">{r.trigger}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function AutoOrganize() {
  const [config, setConfig] = useState(null);
  const [view, setView] = useState(null);
  const [log, setLog] = useState({ moves: [], runs: [] });
  const [error, setError] = useState(null);
  const [tab, setTab] = useState("preview");
  const [plan, setPlan] = useState(null);
  const [busy, setBusy] = useState(false);
  const [drawer, setDrawer] = useState({ open: false, rule: null, index: -1 });
  const [dragFrom, setDragFrom] = useState(null);
  const [dragOver, setDragOver] = useState(null);
  const activityRef = useRef(null);

  const refresh = useCallback(async () => {
    try {
      const [c, v, l] = await Promise.all([
        apiService.getAutoOrganize(), apiService.getAutoOrganizeState(), apiService.getAutoOrganizeLog(),
      ]);
      setConfig(c);
      setView(v);
      setLog(l);
      setError(null);
    } catch (e) {
      setError(e.message);
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(() => {
      apiService.getAutoOrganizeState().then(setView).catch(() => {});
      apiService.getAutoOrganizeLog().then(setLog).catch(() => {});
    }, 30000);
    return () => clearInterval(timer);
  }, [refresh]);

  if (error && !config) {
    return <div className="ao"><div className="ao-wrap"><div className="ao-alert">{error}</div></div></div>;
  }
  if (!config || !view) return <Spinner />;

  const folders = view.folders;
  const live = config.settings.mode === "live";
  const st = statusText(config.settings, view.status);
  const viewOf = (id) => view.rules.find((r) => r.ruleId === id);

  const save = async (next) => {
    try {
      const saved = await apiService.saveAutoOrganize({ ...next, revision: config.revision });
      setConfig(saved);
      apiService.getAutoOrganizeState().then(setView).catch(() => {});
      return saved;
    } catch (e) {
      if (e.status === 409) {
        toast.warn("The rules were changed in another tab. Reloaded the latest version.");
        await refresh();
      }
      throw e;
    }
  };
  const saveQuiet = (next) => save(next).catch((e) => e.status !== 409 && toast.error(e.message));

  const setSettings = (patch) => saveQuiet({ ...config, settings: { ...config.settings, ...patch } });
  const setRules = (rules) => saveQuiet({ ...config, rules });

  const runPreview = async (ruleId) => {
    setBusy(true);
    try {
      const p = await apiService.previewAutoOrganize(ruleId ? { ruleId } : {});
      setPlan(p);
      setTab("preview");
      activityRef.current && activityRef.current.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const runNow = async () => {
    setBusy(true);
    try {
      if (live) {
        const p = await apiService.previewAutoOrganize({});
        if (p.moves.length === 0) {
          toast.info("Nothing to move right now.");
          setPlan(p);
          setTab("preview");
          return;
        }
        // eslint-disable-next-line no-alert
        if (!window.confirm(`This will move ${p.moves.length} document${p.moves.length === 1 ? "" : "s"}. Continue?`)) return;
      }
      const res = await apiService.runAutoOrganizeNow();
      const r = res.run;
      if (r.mode === "LIVE") toast.success(`Sweep done: ${r.moved} moved, ${r.errors} errors`);
      else toast.info(`Dry sweep: ${r.planned} would move`);
      setPlan(res.plan);
      setTab(r.mode === "LIVE" ? "moves" : "preview");
      await refresh();
    } catch (e) {
      toast.error(e.status === 409 ? `Not run: ${e.message}` : e.message);
    } finally {
      setBusy(false);
    }
  };

  const undo = async (m) => {
    setBusy(true);
    try {
      await apiService.undoAutoOrganizeMove(m.id);
      toast.success(`Moved “${m.docName}” back to ${m.fromName}`);
      await refresh();
    } catch (e) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };
  const reapply = async (holdId) => {
    setBusy(true);
    try {
      const entry = await apiService.reapplyAutoOrganizeHold(holdId);
      toast.success(`Moved “${entry.docName}” to ${entry.toName}`);
      setPlan(null);
      await refresh();
    } catch (e) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const rules = config.rules;
  const openDrawer = (rule, index) => setDrawer({ open: true, rule: JSON.parse(JSON.stringify(rule)), index });
  const closeDrawer = () => setDrawer((d) => ({ ...d, open: false }));
  const saveRule = async (rule) => {
    const named = withDisplayNames(rule, folders);
    const next = [...rules];
    if (drawer.index >= 0) next[drawer.index] = named;
    else next.push(named);
    try {
      await save({ ...config, rules: next });
    } catch (e) {
      if (e.status === 409) {
        closeDrawer();
        return;
      }
      throw e;
    }
    closeDrawer();
    toast.success("Rule saved");
  };
  const deleteRule = (index) => {
    // eslint-disable-next-line no-alert
    if (!window.confirm(`Delete rule ${index + 1}?`)) return;
    setRules(rules.filter((_, i) => i !== index));
    closeDrawer();
  };
  const moveRule = (index, delta) => {
    const to = index + delta;
    if (to < 0 || to >= rules.length) return;
    const next = [...rules];
    const [r] = next.splice(index, 1);
    next.splice(to, 0, r);
    setRules(next);
  };
  const drag = {
    over: dragOver,
    setOver: setDragOver,
    start: setDragFrom,
    drop: (to) => {
      setDragOver(null);
      if (dragFrom === null || dragFrom === to) return;
      const next = [...rules];
      const [r] = next.splice(dragFrom, 1);
      next.splice(to, 0, r);
      setDragFrom(null);
      setRules(next);
    },
  };
  const sharesSource = (rule) => rules.filter((r) => r.enabled && r.source.id === rule.source.id).length > 1;
  const otherRules = rules.map((r, i) => ({ ...r, _number: i + 1 })).filter((_, i) => i !== drawer.index);

  return (
    <div className="ao">
      <div className="ao-wrap">
        {/* header bar */}
        <div className="ao-hdr">
          <h1>Auto-organize</h1>
          <span className={`ao-status ${st.cls}`}><span className="dot" />{st.text}</span>
          <span className="ao-spacer" />
          <label className="ao-sw master" title={config.settings.enabled ? "Sweeper on" : "Sweeper off"}>
            <input type="checkbox" checked={config.settings.enabled} aria-label="Sweeper on/off"
              onChange={(e) => setSettings({ enabled: e.target.checked })} />
            <i />
          </label>
          <div className="ao-seg" title="Mode" role="radiogroup" aria-label="Mode">
            <label className={live ? "on" : ""}>
              <input type="radio" name="ao-mode" checked={live} onChange={() => setSettings({ mode: "live" })} /><span>Live</span>
            </label>
            <label className={`dry ${live ? "" : "on"}`}>
              <input type="radio" name="ao-mode" checked={!live} onChange={() => setSettings({ mode: "dry" })} /><span>Dry-run</span>
            </label>
          </div>
          <span className="ao-every">Every
            <select className="ao-inline-sel" value={config.settings.intervalMinutes} aria-label="Sweep interval"
              onChange={(e) => setSettings({ intervalMinutes: parseInt(e.target.value, 10) })}>
              {INTERVALS.map((m) => <option key={m} value={m}>{m} min</option>)}
            </select>
          </span>
          <span className="ao-acts">
            <button className="ao-btn" disabled={busy} onClick={() => runPreview()}>Preview</button>
            <button className="ao-btn primary" disabled={busy || !config.settings.enabled || view.status.running}
              title={config.settings.enabled ? "" : "Turn the sweeper on to run"} onClick={runNow}>
              {live ? "Run now" : "Run dry sweep"}
            </button>
          </span>
        </div>

        {/* rules */}
        <div className="ao-card">
          <div className="ao-card-h">
            <h2>Rules</h2>
            <span className="ao-hint">Run top to bottom · first match wins per document per sweep</span>
            <button className="ao-tip" title="Each sweep is one pass over the rules in this order. A document is claimed by the first rule that matches it and is not evaluated by later rules in that sweep. A document the sweeper moved out of a folder in the last 24 h is never moved back into it, so two opposite rules can ping-pong at most once. Documents tagged “no automation” are always skipped.">?</button>
          </div>
          {rules.length === 0 ? (
            <div className="ao-empty">
              No rules yet. The sweeper does nothing until you add one.
              <div><button className="ao-btn primary" style={{ marginTop: 12 }} onClick={() => openDrawer(exampleRule(folders), -1)}>Start with the example rule</button></div>
              <div className="ex"><b>Research Reports</b> → finished reading, at least 3 pages, settled 6 h → <b>Research Reports / Read</b></div>
            </div>
          ) : rules.map((r, i) => (
            <RuleRow key={r.id} rule={r} index={i} count={rules.length} view={viewOf(r.id)} folders={folders}
              sharesSource={sharesSource(r)} drag={drag}
              onToggle={(on) => setRules(rules.map((x, j) => (j === i ? { ...x, enabled: on } : x)))}
              onEdit={() => openDrawer(r, i)}
              onDuplicate={() => {
                const copy = { ...JSON.parse(JSON.stringify(r)), id: "", name: r.name ? `${r.name} (copy)` : "", enabled: false };
                const next = [...rules];
                next.splice(i + 1, 0, copy);
                setRules(next);
              }}
              onPreview={() => runPreview(r.id)}
              onMove={(d) => moveRule(i, d)}
              onDelete={() => deleteRule(i)} />
          ))}
          <div className="ao-add-row">
            <button className="ao-btn" onClick={() => openDrawer(newRule(folders), -1)}>+ Add rule</button>
            <span className="ao-hint">Pickers only — folders come from the live tree, never typed UUIDs.</span>
          </div>
        </div>

        {/* activity */}
        <div className="ao-card" ref={activityRef}>
          <div className="ao-tabs" role="tablist">
            {[["preview", "Preview"], ["moves", "Moves"], ["runs", "Runs"]].map(([k, l]) => (
              <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>{l}</button>
            ))}
            <span className="right">
              {tab === "preview" && plan && <span className="ao-hint">Preview run {fmtTime(plan.at)} · {plan.scanned} scanned</span>}
              <button className="ao-btn sm" disabled={busy} onClick={() => (tab === "preview" ? runPreview() : refresh())}>Refresh</button>
            </span>
          </div>
          <div className="ao-pane">
            {tab === "preview" && <PreviewPane plan={plan} live={live} busy={busy} onApply={runNow} onReapply={reapply} />}
            {tab === "moves" && <MovesPane moves={log.moves} busy={busy} onUndo={undo} onReapply={reapply} />}
            {tab === "runs" && <RunsPane runs={log.runs} />}
          </div>
        </div>
      </div>

      <RuleDrawer open={drawer.open} initial={drawer.rule} isNew={drawer.index < 0} folders={folders}
        otherRules={otherRules} onCancel={closeDrawer} onSave={saveRule}
        onDelete={() => deleteRule(drawer.index)}
        onShowPreview={(p) => {
          setPlan(p);
          setTab("preview");
          closeDrawer();
          setTimeout(() => activityRef.current && activityRef.current.scrollIntoView({ behavior: "smooth" }), 50);
        }} />
    </div>
  );
}
