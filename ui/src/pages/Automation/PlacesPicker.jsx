import { useState } from "react";

const ICON = { folder: "📁", document: "📓" };

// The "In" row of a page rule: chips for the chosen folders and documents,
// "+ add" opens a searchable list. Nothing chosen = anywhere. Items come from
// GET /watchables; a selection is stored by id (name is a display cache).
export default function PlacesPicker({ items, selected, onChange }) {
  const [open, setOpen] = useState(false);
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
    if (s.kind === "folder" && s.id === "") return "My files (anywhere)";
    const hit = list.find((i) => i.id === s.id && i.kind === s.kind);
    return hit ? hit.path : s.name || s.id;
  };
  return (
    <div className="ao-places">
      <div className="ao-frow" aria-live="polite">
        <span className="ao-lbl">In</span>
        {selected.map((s) => (
          <span className="ao-wchip" key={`${s.kind}:${s.id}`} title={label(s)}>
            <span aria-hidden="true">{ICON[s.kind]}</span>
            <span className="n">{label(s)}</span>
            <button type="button" aria-label={`Stop watching ${label(s)}`} onClick={() => onChange(selected.filter((x) => x !== s))}>×</button>
          </span>
        ))}
        {selected.length === 0 && <span className="ao-any">anywhere</span>}
        <button type="button" className="ao-link" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "done" : "+ add"}</button>
        <span className="ao-hint">(empty = anywhere)</span>
      </div>
      {open && (
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
      )}
    </div>
  );
}
