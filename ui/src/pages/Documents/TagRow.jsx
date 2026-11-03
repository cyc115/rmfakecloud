import { useState } from "react";
import { Badge, CloseButton } from "react-bootstrap";

// TagRow one editable row of tags: chips with a remove button and a "+ tag"
// field (Enter adds, Escape clears). listId names a <datalist> of suggestions.
export default function TagRow({ label, tags, onAdd, onRemove, listId, busy, bg = "secondary", text }) {
  const [draft, setDraft] = useState("");
  const submit = () => {
    const t = draft.trim();
    if (!t) return;
    onAdd(t);
    setDraft("");
  };
  return (
    <span className="tag-row" style={{ display: "inline-flex", flexWrap: "wrap", alignItems: "center", gap: "6px" }}>
      <span className="text-secondary">{label}:</span>
      {tags.map((t) => (
        <Badge key={t} bg={bg} text={text} style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
          {t}
          <CloseButton aria-label={`Remove tag ${t}`} disabled={busy} onClick={() => onRemove(t)}
            style={{ fontSize: "0.5rem", padding: "2px" }} variant={bg === "info" ? undefined : "white"} />
        </Badge>
      ))}
      <input type="text" list={listId} placeholder="+ tag" aria-label={`Add a tag to ${label.toLowerCase()}`}
        value={draft} disabled={busy} onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") { e.preventDefault(); submit(); }
          else if (e.key === "Escape") setDraft("");
        }}
        style={{ width: "8em", background: "transparent", color: "inherit", fontSize: "0.85em",
          border: "1px solid var(--bs-border-color, #555)", borderRadius: "4px", padding: "1px 4px" }} />
    </span>
  );
}
