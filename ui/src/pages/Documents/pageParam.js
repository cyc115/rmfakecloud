// Viewer page number carried in the URL: /documents/<id>?page=N (1-based).

// parsePageParam the page from a location search string; null when absent or
// not a positive whole number
export function parsePageParam(search) {
  const v = new URLSearchParams(search || "").get("page");
  if (v === null || !/^\d+$/.test(v.trim())) return null;
  const n = Number(v);
  return n >= 1 ? n : null;
}

// clampPage keeps n within 1..numPages; null/invalid n gives 1
export function clampPage(n, numPages) {
  const max = Math.max(1, numPages || 1);
  if (!Number.isInteger(n)) return 1;
  return Math.min(Math.max(n, 1), max);
}

// withPageParam the search string with page=n set, other params kept
export function withPageParam(search, n) {
  const p = new URLSearchParams(search || "");
  p.set("page", String(n));
  return "?" + p.toString();
}

// parsePageInput the page typed into the viewer's page field, clamped to
// 1..numPages; null when the text is not a whole number (the field resets)
export function parsePageInput(text, numPages) {
  const t = String(text ?? "").trim();
  if (!/^\d+$/.test(t)) return null;
  return clampPage(Number(t), numPages);
}
