// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { findPseudoNode } from "./pseudoTree.js";

const entries = [
  {
    id: "root", name: "My Files", isFolder: true, children: [
      {
        id: "a", name: "A", isFolder: true, children: [
          { id: "b", name: "B", isFolder: true, children: [
            { id: "f", name: "f.pdf", isFolder: false },
          ] },
        ],
      },
    ],
  },
  { id: "trash", name: "Trash", isFolder: true, children: [] },
];

// Every folder reachable from the selected node (breadcrumb parents, children)
// can be opened in the Folder pane, which reads node.children.
function assertFoldersHaveChildren(node) {
  for (let n = node; n; n = n.parent) {
    if (!n.isLeaf) assert.ok(Array.isArray(n.children), `${n.id} has no children array`);
  }
}

test("breadcrumb parents of a deep-linked file are openable folders", () => {
  const f = findPseudoNode(entries, "f");
  assert.equal(f.isLeaf, true);
  assert.deepEqual(
    [f.parent.id, f.parent.parent.id, f.parent.parent.parent.id],
    ["b", "a", "root"],
  );
  assertFoldersHaveChildren(f);
  assert.equal(f.parent.parent.children[0].id, "b");
});

test("root sits under react-arborist's hidden root, which NameTag stops at", () => {
  const root = findPseudoNode(entries, "root");
  assert.equal(root.isRoot, true);
  assert.equal(root.parent.id, "__REACT_ARBORIST_INTERNAL_ROOT__");
});

test("children of a deep-linked folder keep their own children and parent", () => {
  const a = findPseudoNode(entries, "a");
  const b = a.children[0];
  assert.equal(b.parent, a);
  assert.equal(b.children[0].id, "f");
  assert.equal(b.children[0].data.name, "f.pdf");
});

test("a folder whose API children are null still gets an empty array", () => {
  const trash = findPseudoNode([{ id: "trash", name: "Trash", isFolder: true, children: null }], "trash");
  assert.deepEqual(trash.children, []);
});

test("unknown id returns null", () => {
  assert.equal(findPseudoNode(entries, "nope"), null);
});
