import { useState } from "react";

const ICON = { folder: "📁", document: "📓" };

// Multi-select of folders and documents to watch. Items come from
// GET /watchables; a selection is stored by id (name is a display cache).
export default function WatchPicker({ items, selected, onChange }) {
  const [term, setTerm] = useState("");
  const t = term.trim().toLowerCase();
  const list = items || [];
  const shown = list.filter((i) => !t || i.path.toLowerCase().includes(t));
  const isSel = (i) => selected.some((s) => s.id === i.id && s.kind === i.kind);
  const toggle = (i) => {
    if (isSel(i)) onChange(selected.filter((s) => !(s.id === i.id && s.kind === i.kind)));
    else onChange([...selected, { id: i.id, kind: i.kind, name: i.name }]);
  };
  const label = (s) => {
    const hit = list.find((i) => i.id === s.id && i.kind === s.kind);
    return hit ? hit.path : s.name || s.id;
  };
  return (
    <div className="ao-watch">
      <div className="ao-wsel" aria-live="polite">
        {selected.length === 0 ? (
          <span className="ao-hint">Nothing watched yet</span>
        ) : (
          <>
            <span className="ao-wcount">Watching {selected.length} item{selected.length === 1 ? "" : "s"}</span>
            {selected.map((s) => (
              <span className="ao-wchip" key={`${s.kind}:${s.id}`} title={label(s)}>
                <span aria-hidden="true">{ICON[s.kind]}</span>
                <span className="n">{label(s)}</span>
                <button type="button" aria-label={`Stop watching ${label(s)}`} onClick={() => onChange(selected.filter((x) => x !== s))}>×</button>
              </span>
            ))}
          </>
        )}
      </div>
      <div className="ao-pick">
        <div className="ao-search">
          <span aria-hidden="true">🔍</span>
          <input type="text" placeholder="Search folders and notebooks" value={term} aria-label="Search watch items"
            onChange={(e) => setTerm(e.target.value)} />
        </div>
        <ul role="listbox" aria-multiselectable="true" aria-label="watch items">
          {shown.map((i) => (
            <li key={`${i.kind}:${i.id}`} role="option" aria-selected={isSel(i)} aria-label={i.path}
              className={isSel(i) ? "sel" : ""} onClick={() => toggle(i)}>
              <span className="f" aria-hidden="true">{isSel(i) ? "☑" : "☐"}</span>
              <span aria-hidden="true">{ICON[i.kind]}</span>
              <span className="p" aria-hidden="true">{i.path}</span>
              {i.kind === "document" && i.docType && i.docType !== "notebook" && (
                <span className="ao-chip" aria-hidden="true">{i.docType}</span>
              )}
            </li>
          ))}
          {shown.length === 0 && <li className="ao-none">{list.length ? "Nothing matches" : "No folders or notebooks"}</li>}
        </ul>
      </div>
    </div>
  );
}
