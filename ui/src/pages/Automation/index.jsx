import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "react-toastify";
import apiService from "../../services/api.service";
import Spinner from "../../components/Spinner";
import RuleEditor from "./RuleEditor";
import Sentence from "./Sentence";
import {
  INTERVALS, SKIP_CHIP, SKIP_LABELS, exampleRule, fmtAgo, fmtTime, isPipeline, missingFor, newPipelineRule,
  pendingText, runCounts, sentenceText, withDisplayNames,
} from "./rules";
import "./Automation.scss";

// modePill the header pill: Live / Dry-run and how often rules are checked
function modePill(settings, status) {
  if (status.running) return { cls: "running", text: "Running now…" };
  if (!settings.enabled) return { cls: "paused", text: "Paused" };
  const live = settings.mode === "live";
  return { cls: live ? "" : "dry", text: `${live ? "Live" : "Dry-run"} · checks every ${settings.intervalMinutes} min` };
}

// headerStatus the sweeper and page-watch lines under the header controls
function headerStatus(settings, status, pipeline, live) {
  const out = [];
  const last = status.lastSweep ? `Documents: last check ${fmtTime(status.lastSweep.at)}` : "Documents: not checked yet";
  const next = settings.enabled && status.nextAt ? ` · next ${fmtTime(status.nextAt)}` : "";
  out.push({ cls: "", text: last + next });
  if (pipeline) {
    if (!pipeline.enabled) {
      out.push({ cls: "muted", text: "Pages: page watch is off on this server; page rules are saved but nothing is sent." });
    } else {
      let t = pipeline.lastRun ? `Pages: last check ${fmtTime(pipeline.lastRun.at)}` : "Pages: not checked yet";
      if (pipeline.nextAt) t += ` · next ${fmtTime(pipeline.nextAt)}`;
      out.push({ cls: "", text: t });
      if (pipeline.lastError) out.push({ cls: "bad", text: `Last page check failed: ${pipeline.lastError}` });
      if (pipeline.pendingPipeline > 0) out.push({ cls: "muted", text: pendingText(pipeline.pendingPipeline, live) });
    }
  }
  return out;
}

// ruleStatus the line under a rule: last run, what it did, errors
function ruleStatus({ rule, view, moves, pipeline, sweep, missing, sharesSource }) {
  const out = [];
  if (isPipeline(rule)) {
    if (!rule.enabled) out.push({ text: "paused" });
    else if (!pipeline || !pipeline.enabled) out.push({ text: "page watch is off on this server" });
    else if (pipeline.lastError) out.push({ cls: "bad", text: `last check failed: ${pipeline.lastError}` });
    else if (pipeline.lastRun) {
      out.push({ cls: "ok", text: `● ran ${fmtAgo(pipeline.lastRun.at)}` });
      out.push({ text: runCounts(pipeline.lastRun) });
    } else out.push({ text: "not run yet" });
    for (const m of missing) {
      const w = (rule.watch || []).find((x) => x.id === m.watchId) || {};
      out.push({ cls: "warn", text: `${w.name || m.name || m.watchId}: ${m.inTrash ? "watched item is in the Trash" : "watched item no longer exists"}` });
    }
    return out;
  }
  if (!rule.enabled) {
    out.push({ text: "paused" });
  } else {
    out.push(sweep ? { cls: "ok", text: `● ran ${fmtAgo(sweep.at)}` } : { text: "not run yet" });
  }
  if (view) {
    for (const p of view.problems) out.push({ cls: "bad", text: p });
    if (rule.enabled && view.problems.length === 0) out.push({ text: view.matches > 0 ? `${view.matches} would move now` : "nothing to move now" });
    if (view.holds > 0) out.push({ text: `${view.holds} held` });
    if (view.optedOut > 0) out.push({ cls: "tag", text: `${view.optedOut} opted out` });
    if (view.destWillCreate) out.push({ text: "folder created on first live move" });
    if (view.loopsWith && view.loopsWith.length) out.push({ cls: "warn", text: `loops with rule ${view.loopsWith.join(", ")}` });
    if (view.sourceRenamedFrom) out.push({ text: `folder renamed from “${view.sourceRenamedFrom}”` });
    if (view.destRenamedFrom) out.push({ text: `destination renamed from “${view.destRenamedFrom}”` });
  }
  const last = moves.find((m) => m.ruleId === rule.id && m.kind !== "undo");
  if (last) {
    out.push({
      text: `${last.mode === "LIVE" ? "moved" : "would have moved"} ${last.docName} ${fmtAgo(last.at)}${last.undoneAt ? " · undone" : ""}`,
      undo: !last.holdId && last.canUndo && !last.undoneAt ? last : null,
    });
  }
  if (sharesSource) out.push({ cls: "order", text: "order matters: shares a folder with another rule" });
  return out;
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

function RuleRow({ rule, index, count, ctx, status, busy, onUndo, onToggle, onEdit, onDuplicate, onPreview, onMove, onDelete, drag }) {
  const custom = rule.name && rule.name !== sentenceText(rule, ctx) ? rule.name : "";
  return (
    <div className={`ao-rule ${rule.enabled ? "" : "off"} ${drag.over === index ? "dragover" : ""}`}
      onDragOver={(e) => { e.preventDefault(); drag.setOver(index); }}
      onDragLeave={() => drag.setOver(null)}
      onDrop={(e) => { e.preventDefault(); drag.drop(index); }}>
      <span className="ao-grip" title="Drag to reorder" draggable
        onDragStart={(e) => { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", String(index)); drag.start(index); }}
        onDragEnd={() => drag.setOver(null)}>⋮⋮</span>
      <label className="ao-sw" title={rule.enabled ? "On" : "Off"}>
        <input type="checkbox" checked={rule.enabled} onChange={(e) => onToggle(e.target.checked)} aria-label={`Rule ${index + 1} on`} />
        <i />
      </label>
      <div className="ao-sent">
        {custom && <div className="ao-rname">{custom}</div>}
        <Sentence rule={rule} ctx={ctx} onClick={onEdit} />
        <div className="ao-meta">
          {status.map((s, i) => (
            <span key={i} className={`ao-ms ${s.cls || ""}`}>
              {i > 0 && <span className="sep"> · </span>}
              {s.text}
              {s.undo && (
                <>
                  <span className="sep"> · </span>
                  <button type="button" className="ao-link" disabled={busy} onClick={() => onUndo(s.undo)}>undo</button>
                </>
              )}
            </span>
          ))}
        </div>
      </div>
      <Kebab items={[
        { label: "Edit", onClick: onEdit },
        { label: "Duplicate", onClick: onDuplicate },
        { label: "Preview this rule", onClick: onPreview, disabled: isPipeline(rule) },
        "-",
        { label: "Move up", onClick: () => onMove(-1), disabled: index === 0 },
        { label: "Move down", onClick: () => onMove(1), disabled: index === count - 1 },
        "-",
        { label: "Delete", onClick: onDelete, danger: true },
      ]} />
    </div>
  );
}

function PreviewPane({ plan, partial, live, onApply, onPreviewAll, onReapply, busy }) {
  if (!plan) {
    return <div className="ao-empty-pane">Press <b>Preview</b> to see what the next check would do. Nothing moves.</div>;
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
        {partial ? (
          <>
            {/* applying runs every document rule, so it is offered only on a full preview */}
            <button className="ao-btn" disabled={busy} onClick={onPreviewAll}>Preview all rules</button>
            <span className="ao-hint">This preview covers one rule. A run applies every document rule in order, so preview all rules to apply.</span>
          </>
        ) : (
          <>
            <button className="ao-btn primary" disabled={!live || plan.moves.length === 0 || busy} onClick={onApply}>
              Apply {plan.moves.length === 1 ? "this move" : `these ${plan.moves.length} moves`}
            </button>
            <span className="ao-hint">
              {live ? `Live: this will move ${plan.moves.length} document${plan.moves.length === 1 ? "" : "s"}. You will be asked to confirm.`
                : "Switch to Live to apply. In Dry-run the preview is informational."}
            </span>
          </>
        )}
      </div>
    </>
  );
}

function MovesPane({ moves, onUndo, onReapply, busy }) {
  if (!moves.length) return <div className="ao-empty-pane">No moves yet.</div>;
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
      <div className="ao-grp"><span className="ao-hint">Last 50 moves made by rules. Manual moves from the tablet are not listed. Undo reverses the move and sets a hold so the rule does not re-move it while its trigger inputs are unchanged.</span></div>
    </>
  );
}

function RunsPane({ runs }) {
  if (!runs.length) return <div className="ao-empty-pane">No document checks yet.</div>;
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

function PageRunsPane({ pipeline }) {
  if (!pipeline || !pipeline.enabled) return <div className="ao-empty-pane">Page watch is off on this server. Page rules are saved but nothing is sent.</div>;
  const runs = pipeline.runs || [];
  if (!runs.length) return <div className="ao-empty-pane">No page checks yet.</div>;
  return (
    <div className="ao-pruns">
      {runs.map((r) => (
        <div className={`ao-prun ${r.error ? "bad" : ""}`} key={`${r.at}-${r.trigger}`}>
          <span className="when">{fmtTime(r.at)}</span>
          <span className={`ao-chip ${r.mode === "live" ? "live" : "dry"}`}>{r.mode}</span>
          <span className="trig">{r.trigger}</span>
          <span className="nums">{runCounts(r)} · {r.skipped} skipped</span>
          {r.error && <span className="errbox">{r.error}</span>}
        </div>
      ))}
    </div>
  );
}

export default function Automation() {
  const [config, setConfig] = useState(null);
  const [view, setView] = useState(null);
  const [log, setLog] = useState({ moves: [], runs: [] });
  const [error, setError] = useState(null);
  const [tab, setTab] = useState("preview");
  const [plan, setPlan] = useState(null);
  // partial: the plan covers one rule or an unsaved draft, not the whole sweep
  const [planPartial, setPlanPartial] = useState(false);
  const [busy, setBusy] = useState(false);
  const [drawer, setDrawer] = useState({ open: false, rule: null, index: -1 });
  const [pipeline, setPipeline] = useState(null);
  const [watchables, setWatchables] = useState([]);
  const [pageTags, setPageTags] = useState([]);
  const [dragFrom, setDragFrom] = useState(null);
  const [dragOver, setDragOver] = useState(null);
  const activityRef = useRef(null);
  // saves are serialized and applied to the latest saved config, so quick
  // consecutive edits never PUT a stale revision
  const configRef = useRef(null);
  const saveChain = useRef(Promise.resolve());

  const refreshPipeline = useCallback(() => apiService.getPipeline().then(setPipeline).catch(() => {}), []);

  const refresh = useCallback(async () => {
    try {
      const [c, v, l] = await Promise.all([
        apiService.getAutoOrganize(), apiService.getAutoOrganizeState(), apiService.getAutoOrganizeLog(),
      ]);
      configRef.current = c;
      setConfig(c);
      setView(v);
      setLog(l);
      setError(null);
    } catch (e) {
      setError(e.message);
      return;
    }
    // the page-watch extras are optional: the page works without them
    refreshPipeline();
    apiService.getWatchables().then((w) => setWatchables(w.items)).catch(() => {});
  }, [refreshPipeline]);

  useEffect(() => {
    refresh();
    const timer = setInterval(() => {
      apiService.getAutoOrganizeState().then(setView).catch(() => {});
      apiService.getAutoOrganizeLog().then(setLog).catch(() => {});
      refreshPipeline();
    }, 30000);
    return () => clearInterval(timer);
  }, [refresh, refreshPipeline]);

  if (error && !config) {
    return <div className="ao"><div className="ao-wrap"><div className="ao-alert">{error}</div></div></div>;
  }
  if (!config || !view) return <Spinner />;

  const folders = view.folders;
  const ctx = { folders, watchables };
  const live = config.settings.mode === "live";
  const pill = modePill(config.settings, view.status);

  // update: (latest config) => next config
  const save = (update) => {
    const run = async () => {
      const cur = configRef.current;
      try {
        const saved = await apiService.saveAutoOrganize({ ...update(cur), revision: cur.revision });
        configRef.current = saved;
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
    const p = saveChain.current.then(run, run);
    saveChain.current = p.catch(() => {});
    return p;
  };
  const saveQuiet = (update) => save(update).catch((e) => e.status !== 409 && toast.error(e.message));

  const setSettings = (patch) => saveQuiet((c) => ({ ...c, settings: { ...c.settings, ...patch } }));
  // updateRules: (latest rules) => next rules
  const setRules = (updateRules) => saveQuiet((c) => ({ ...c, rules: updateRules(c.rules) }));

  // request: {} for all rules, {ruleId} for one, {rule} for a draft (a disabled rule previews as a draft)
  const runPreview = async (request) => {
    setBusy(true);
    try {
      const p = await apiService.previewAutoOrganize(request || {});
      setPlan(p);
      setPlanPartial(!!(request && (request.ruleId || request.rule)));
      setTab("preview");
      activityRef.current && activityRef.current.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const rules = config.rules;
  const hasMoveRules = rules.some((r) => r.enabled && !isPipeline(r));
  const hasPageRules = rules.some((r) => r.enabled && isPipeline(r));

  // sweep: the document rules; false when the user cancelled the live confirm
  const sweep = async () => {
    if (live) {
      const p = await apiService.previewAutoOrganize({});
      if (p.moves.length === 0) {
        toast.info("Nothing to move right now.");
        setPlan(p);
        setPlanPartial(false);
        setTab("preview");
        return true;
      }
      // eslint-disable-next-line no-alert
      if (!window.confirm(`This will move ${p.moves.length} document${p.moves.length === 1 ? "" : "s"}. Continue?`)) return false;
    }
    const res = await apiService.runAutoOrganizeNow();
    const r = res.run;
    if (r.mode === "LIVE") toast.success(`Documents checked: ${r.moved} moved, ${r.errors} errors`);
    else toast.info(`Dry run: ${r.planned} would move`);
    setPlan(res.plan);
    setPlanPartial(false);
    setTab(r.mode === "LIVE" ? "moves" : "preview");
    return true;
  };

  const checkPages = async () => {
    const r = await apiService.runPipelineNow();
    if (r.error) toast.error(`Page check: ${r.error}`);
    else toast.success(`Pages checked: ${runCounts(r)}`);
    if (!hasMoveRules) setTab("pages");
  };

  // Run now: the document rules (sweep) and the page rules (page watch)
  const runNow = async () => {
    setBusy(true);
    try {
      const pages = hasPageRules && pipeline && pipeline.enabled;
      if (hasMoveRules || !pages) {
        if (!(await sweep())) return;
      }
      if (pages) await checkPages();
      await refresh();
    } catch (e) {
      toast.error(e.status === 409 ? `Not run: ${e.message}` : e.message);
    } finally {
      setBusy(false);
      refreshPipeline();
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

  const openDrawer = (rule, index) => {
    // one after the other: both read the tree, whose root blob lock is not shared
    apiService.getWatchables().then((w) => setWatchables(w.items)).catch(() => {})
      .then(() => apiService.getPageTags()).then((t) => t && setPageTags(t.tags)).catch(() => {});
    setDrawer({ open: true, rule: JSON.parse(JSON.stringify(rule)), index });
  };
  const closeDrawer = () => setDrawer((d) => ({ ...d, open: false }));
  const saveRule = async (rule) => {
    const named = withDisplayNames(rule, folders);
    const editing = drawer.index >= 0 && named.id;
    try {
      await save((c) => {
        const next = [...c.rules];
        const i = editing ? next.findIndex((r) => r.id === named.id) : -1;
        if (i >= 0) next[i] = named;
        else next.push(named);
        return { ...c, rules: next };
      });
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
    const id = rules[index] && rules[index].id;
    // eslint-disable-next-line no-alert
    if (!id || !window.confirm(`Delete rule ${index + 1}?`)) return;
    setRules((cur) => cur.filter((r) => r.id !== id));
    closeDrawer();
  };
  // move the rule with id to position of (its index + delta), or onto targetId
  const reorder = (id, delta, targetId) => setRules((cur) => {
    const from = cur.findIndex((r) => r.id === id);
    const to = targetId !== undefined ? cur.findIndex((r) => r.id === targetId) : from + delta;
    if (from < 0 || to < 0 || to >= cur.length || from === to) return cur;
    const next = [...cur];
    const [r] = next.splice(from, 1);
    next.splice(to, 0, r);
    return next;
  });
  const drag = {
    over: dragOver,
    setOver: setDragOver,
    start: setDragFrom,
    drop: (to) => {
      setDragOver(null);
      if (dragFrom === null || dragFrom === to) return;
      const fromId = rules[dragFrom].id;
      setDragFrom(null);
      reorder(fromId, 0, rules[to].id);
    },
  };
  const sharesSource = (rule) => !isPipeline(rule) && rule.enabled &&
    rules.filter((r) => r.enabled && !isPipeline(r) && r.source.id === rule.source.id).length > 1;
  const otherRules = rules.map((r, i) => ({ ...r, _number: i + 1 })).filter((r, i) => i !== drawer.index && !isPipeline(r));
  const example = exampleRule(folders);

  return (
    <div className="ao">
      <div className="ao-wrap">
        <div className="ao-card ao-main">
          <div className="ao-hdr">
            <div className="ao-hdr-top">
              <h1>Automation</h1>
              <span className="ao-spacer" />
              <span className={`ao-status ${pill.cls}`}><span className="dot" />{pill.text}</span>
              <button className="ao-btn primary" onClick={() => openDrawer(newPipelineRule(pipeline && pipeline.defaults), -1)}>+ New</button>
            </div>
            <div className="ao-hdr-ctl">
              <label className="ao-sw master" title={config.settings.enabled ? "Automation on" : "Automation off"}>
                <input type="checkbox" checked={config.settings.enabled} aria-label="Automation on/off"
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
                <select className="ao-inline-sel" value={config.settings.intervalMinutes} aria-label="Check interval"
                  onChange={(e) => setSettings({ intervalMinutes: parseInt(e.target.value, 10) })}>
                  {INTERVALS.map((m) => <option key={m} value={m}>{m} min</option>)}
                </select>
              </span>
              <span className="ao-spacer" />
              <span className="ao-acts">
                <button className="ao-btn" disabled={busy} onClick={() => runPreview()}>Preview</button>
                <button className="ao-btn" disabled={busy || !config.settings.enabled || view.status.running}
                  title={config.settings.enabled ? "Check every rule now" : "Turn automation on to run"} onClick={runNow}>
                  Run now
                </button>
              </span>
            </div>
            <div className="ao-hdr-st">
              {headerStatus(config.settings, view.status, pipeline, live).map((s) => (
                <div key={s.text} className={`ao-hs ${s.cls}`}>{s.text}</div>
              ))}
            </div>
          </div>

          <div className="ao-rules">
            {rules.length === 0 ? (
              <div className="ao-empty">
                No automations yet. Nothing runs until you add one.
                <div className="ex"><Sentence rule={example} ctx={ctx} /></div>
                <div><button className="ao-btn" style={{ marginTop: 12 }} onClick={() => openDrawer(example, -1)}>Start with this example</button></div>
              </div>
            ) : rules.map((r, i) => (
              <RuleRow key={r.id} rule={r} index={i} count={rules.length} ctx={ctx} busy={busy} drag={drag}
                status={ruleStatus({
                  rule: r, view: view.rules.find((x) => x.ruleId === r.id), moves: log.moves || [], pipeline,
                  sweep: view.status.lastSweep, missing: missingFor(pipeline, r.id), sharesSource: sharesSource(r),
                })}
                onUndo={undo}
                onToggle={(on) => setRules((cur) => cur.map((x) => (x.id === r.id ? { ...x, enabled: on } : x)))}
                onEdit={() => openDrawer(r, i)}
                onDuplicate={() => {
                  // an auto name stays auto (empty → the next save names it); a custom name gets "(copy)"
                  const custom = r.name && r.name !== sentenceText(r, ctx);
                  const copy = { ...JSON.parse(JSON.stringify(r)), id: "", name: custom ? `${r.name} (copy)` : "", enabled: false };
                  setRules((cur) => {
                    const next = [...cur];
                    next.splice(next.findIndex((x) => x.id === r.id) + 1, 0, copy);
                    return next;
                  });
                }}
                onPreview={() => runPreview(r.enabled ? { ruleId: r.id } : { rule: r })}
                onMove={(d) => reorder(r.id, d)}
                onDelete={() => deleteRule(i)} />
            ))}
          </div>
          <div className="ao-note">
            Every rule is one sentence. Tap a sentence to edit it. Last run, undo and errors live under each rule.
            <button className="ao-tip" title="Document rules run top to bottom: a document is claimed by the first rule that matches it in each check. Page rules run independently. A document a rule moved out of a folder in the last 24 h is never moved back into it, so two opposite rules can ping-pong at most once. Documents tagged “no automation” are always skipped.">?</button>
          </div>
        </div>

        {/* history */}
        <div className="ao-card" ref={activityRef}>
          <div className="ao-tabs" role="tablist">
            {[["preview", "Preview"], ["moves", "Moves"], ["runs", "Runs"], ["pages", "Page checks"]].map(([k, l]) => (
              <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>{l}</button>
            ))}
            <span className="right">
              {tab === "preview" && plan && <span className="ao-hint">Preview run {fmtTime(plan.at)} · {plan.scanned} scanned</span>}
              <button className="ao-btn sm" disabled={busy} onClick={() => (tab === "preview" ? runPreview() : refresh())}>Refresh</button>
            </span>
          </div>
          <div className="ao-pane">
            {tab === "preview" && <PreviewPane plan={plan} partial={planPartial} live={live} busy={busy} onApply={runNow}
              onPreviewAll={() => runPreview()} onReapply={reapply} />}
            {tab === "moves" && <MovesPane moves={log.moves} busy={busy} onUndo={undo} onReapply={reapply} />}
            {tab === "runs" && <RunsPane runs={log.runs} />}
            {tab === "pages" && <PageRunsPane pipeline={pipeline} />}
          </div>
        </div>
      </div>

      <RuleEditor open={drawer.open} initial={drawer.rule} isNew={drawer.index < 0} folders={folders}
        watchables={watchables} pageTags={pageTags} pipelineDefaults={pipeline && pipeline.defaults}
        otherRules={otherRules} onCancel={closeDrawer} onSave={saveRule}
        onDelete={() => deleteRule(drawer.index)}
        onShowPreview={(p) => {
          setPlan(p);
          setPlanPartial(true);
          setTab("preview");
          closeDrawer();
          setTimeout(() => activityRef.current && activityRef.current.scrollIntoView({ behavior: "smooth" }), 50);
        }} />
    </div>
  );
}
