// react-arborist only hands out nodes for visible (expanded) rows, so a page
// opened by URL builds its own node objects with the same shape: id, data,
// isLeaf, isRoot, parent, children, toggle. Every node gets real children and
// a parent link, so any crumb or child tapped from it opens a working folder.
export const ARBORIST_ROOT_ID = "__REACT_ARBORIST_INTERNAL_ROOT__";

function toPseudoNode(item, parent) {
  const node = {
    id: item.id,
    data: item,
    isLeaf: !item.isFolder,
    parent,
    isRoot: item.id === "root" || item.id === "trash",
    toggle: () => {},
  };
  node.children = (item.children || []).map((child) => toPseudoNode(child, node));
  return node;
}

function find(node, id) {
  if (node.id === id) return node;
  for (const child of node.children) {
    const found = find(child, id);
    if (found) return found;
  }
  return null;
}

// Returns the node for `id` with its whole tree built around it, or null.
export function findPseudoNode(entries, id) {
  const top = toPseudoNode({ id: ARBORIST_ROOT_ID, name: "", isFolder: true, children: entries }, null);
  return find(top, id);
}
