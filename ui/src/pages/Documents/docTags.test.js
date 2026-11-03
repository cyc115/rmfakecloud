import { test } from "node:test";
import assert from "node:assert/strict";
import { pageTagsAt, taggedPages } from "./docTags.js";

const dt = { tags: ["Work"], pages: [{ id: "a", tags: [] }, { id: "b", tags: ["To-Do", "extract"] }] };

test("pageTagsAt reads the 1-based page", () => {
  assert.deepEqual(pageTagsAt(dt, 2, 2), ["To-Do", "extract"]);
  assert.deepEqual(pageTagsAt(dt, 1, 2), []);
});

test("pageTagsAt is null when the page list does not match the PDF", () => {
  assert.equal(pageTagsAt(dt, 2, 3), null);
  assert.equal(pageTagsAt({ tags: [], pages: null }, 1, 1), null);
  assert.equal(pageTagsAt(null, 1, 1), null);
  assert.equal(pageTagsAt(dt, 5, 2), null);
});

test("taggedPages lists the pages with tags", () => {
  assert.deepEqual(taggedPages(dt, 2), [{ page: 2, tags: ["To-Do", "extract"] }]);
  assert.deepEqual(taggedPages(dt, 3), []);
});
