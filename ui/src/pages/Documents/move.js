// Drag and drop moves in the document browser: rows of the file list are the
// drag sources; folders (list, grid and the sidebar tree) are the targets.

// MOVE_TYPE the dataTransfer type carrying the moved ids (JSON array)
export const MOVE_TYPE = "application/x-rmfc-move";

// dragIds the items a drag moves: the whole selection when the dragged item
// is part of it, else that item alone
export function dragIds(draggedId, selectedIds) {
  return selectedIds && selectedIds.includes(draggedId) ? [...selectedIds] : [draggedId];
}

// canDrop the ids can move into target: a folder that is not one of them, not
// inside one of them, and not already the parent of every one of them
export function canDrop(target, ids, find) {
  if (!target || !target.data || !target.data.isFolder || !ids.length) return false;
  for (let n = target; n; n = n.parent) {
    if (ids.includes(n.id)) return false;
  }
  return ids.some((id) => find(id)?.parent?.id !== target.id);
}

// parentIdOf the server's parent id of a UI folder: "My Files" is the root ("")
export function parentIdOf(targetId) {
  return targetId === "root" ? "" : targetId;
}

// movedIds the ids a file-list drag carries, or null for other drags (files from the desktop)
export function movedIds(e) {
  if (!e.dataTransfer || !Array.from(e.dataTransfer.types || []).includes(MOVE_TYPE)) return null;
  try { return JSON.parse(e.dataTransfer.getData(MOVE_TYPE) || "[]"); } catch { return []; }
}
