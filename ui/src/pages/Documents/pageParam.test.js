// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePageParam, clampPage, withPageParam } from "./pageParam.js";

test("parsePageParam reads a positive whole number", () => {
  assert.equal(parsePageParam("?page=10"), 10);
  assert.equal(parsePageParam("?a=1&page=2"), 2);
});

test("parsePageParam ignores missing or bad values", () => {
  for (const s of ["", "?", "?page=", "?page=0", "?page=-3", "?page=x", "?page=2.5", "?page=1e3", undefined]) {
    assert.equal(parsePageParam(s), null, String(s));
  }
});

test("clampPage keeps the page within 1..numPages", () => {
  assert.equal(clampPage(2, 5), 2);
  assert.equal(clampPage(99, 5), 5);
  assert.equal(clampPage(0, 5), 1);
  assert.equal(clampPage(null, 5), 1);
  assert.equal(clampPage(3, 0), 1);
});

test("withPageParam sets page and keeps other params", () => {
  assert.equal(withPageParam("", 3), "?page=3");
  assert.equal(withPageParam("?page=1&x=y", 4), "?page=4&x=y");
});
