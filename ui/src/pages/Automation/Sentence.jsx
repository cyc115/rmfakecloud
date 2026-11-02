import { sentence } from "./rules";

// One rule as its sentence: IF/THEN badges, the rule's own words underlined
export default function Sentence({ rule, ctx, onClick }) {
  const parts = sentence(rule, ctx).map((p, i) => {
    if (typeof p === "string") return <span key={i}>{p}</span>;
    if (p.t === "tok") return <span key={i} className="tok">{p.s}</span>;
    // the space after a badge is real text, so the sentence reads (and copies) as words
    return <span key={i}><span className={`ao-${p.t}`}>{p.s}</span>{" "}</span>;
  });
  if (onClick) {
    return <button type="button" className="ao-sentence link" onClick={onClick} title="Edit this rule">{parts}</button>;
  }
  return <div className="ao-sentence">{parts}</div>;
}
