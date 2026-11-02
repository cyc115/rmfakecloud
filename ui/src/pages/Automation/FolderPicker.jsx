import { useState } from "react";

// Pick a folder from the live tree. Folders are chosen, never typed as UUIDs.
export default function FolderPicker({ folders, selected, onSelect, extra, label }) {
  const [term, setTerm] = useState("");
  const t = term.trim().toLowerCase();
  const shown = (folders || []).filter((f) => !t || f.path.toLowerCase().includes(t));
  return (
    <div className="ao-pick">
      <div className="ao-search">
        <span aria-hidden="true">🔍</span>
        <input type="text" placeholder="Search folders" value={term} aria-label={`Search ${label}`}
          onChange={(e) => setTerm(e.target.value)} />
      </div>
      <ul role="listbox" aria-label={label}>
        {shown.map((f) => (
          <li key={f.id || "root"} role="option" aria-selected={selected === f.id}
            className={selected === f.id ? "sel" : ""}
            style={{ paddingLeft: 10 + (t ? 0 : (f.depth || 0) * 18) }}
            onClick={() => onSelect(f)}>
            <span className="f">{selected === f.id ? "▾" : "▸"}</span>
            {t ? f.path : f.name}
          </li>
        ))}
        {shown.length === 0 && <li className="ao-none">No folder matches</li>}
        {extra}
      </ul>
    </div>
  );
}
