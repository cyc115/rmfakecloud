import { test } from "node:test";
import assert from "node:assert/strict";
import { dragIds, canDrop, parentIdOf, movedIds, MOVE_TYPE } from "./move.js";

// a react-arborist-like tree: id, data.isFolder, parent
function tree() {
  const root = { id: "root", data: { isFolder: true }, parent: null };
  const a = { id: "a", data: { isFolder: true }, parent: root };
  const a1 = { id: "a1", data: { isFolder: true }, parent: a };
  const doc = { id: "doc", data: { isFolder: false }, parent: root };
  const trash = { id: "trash", data: { isFolder: true }, parent: null };
  const by = { root, a, a1, doc, trash };
  return { ...by, find: (id) => by[id] };
}

test("dragIds moves the selection when the dragged item is in it", () => {
  assert.deepEqual(dragIds("doc", ["a", "doc"]), ["a", "doc"]);
  assert.deepEqual(dragIds("doc", ["a"]), ["doc"]);
  assert.deepEqual(dragIds("doc", []), ["doc"]);
});

test("canDrop: only into a folder that is not moved or inside a moved folder", () => {
  const t = tree();
  assert.equal(canDrop(t.a, ["doc"], t.find), true);
  assert.equal(canDrop(t.trash, ["doc"], t.find), true);
  assert.equal(canDrop(t.doc, ["a"], t.find), false); // not a folder
  assert.equal(canDrop(t.a, ["a"], t.find), false); // into itself
  assert.equal(canDrop(t.a1, ["a"], t.find), false); // into its own child
  assert.equal(canDrop(t.root, ["doc"], t.find), false); // already there
  assert.equal(canDrop(t.root, ["doc", "a1"], t.find), true); // a1 is not there yet
  assert.equal(canDrop(null, ["doc"], t.find), false);
});

test("parentIdOf maps the UI root to the server's empty parent", () => {
  assert.equal(parentIdOf("root"), "");
  assert.equal(parentIdOf("trash"), "trash");
  assert.equal(parentIdOf("a"), "a");
  assert.equal(typeof MOVE_TYPE, "string");
});

test("movedIds reads a move drag and ignores desktop file drags", () => {
  const ev = (types, data) => ({ dataTransfer: { types, getData: () => data } });
  assert.deepEqual(movedIds(ev(["application/x-rmfc-move"], '["a","b"]')), ["a", "b"]);
  assert.equal(movedIds(ev(["Files"], "")), null);
  assert.deepEqual(movedIds(ev(["application/x-rmfc-move"], "bad")), []);
});
