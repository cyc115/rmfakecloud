import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import apiServices from "./api.service.js";

// Regression test for the de2739a bug family (GlitchTip #86/#99/#152):
// listDocument used to discard handleError's result and call r.json() on
// the 401 error body, so the error JSON flowed into the Documents page and
// crashed it. It must reject without consuming the body, like
// listPasscodeResets does.

let reloads;
let jsonCalls;

function stubFetch(response) {
  globalThis.fetch = async () => response;
}

beforeEach(() => {
  reloads = 0;
  jsonCalls = 0;
  globalThis.window = { location: { reload: () => { reloads += 1; } } };
  globalThis.localStorage = {
    _store: {},
    getItem(k) { return this._store[k] ?? null; },
    setItem(k, v) { this._store[k] = v; },
    removeItem(k) { delete this._store[k]; },
  };
});

test("listDocument rejects on 401 without reading the error body", async () => {
  stubFetch({
    ok: false,
    status: 401,
    headers: { get: () => "application/json" },
    json: async () => { jsonCalls += 1; return { error: "missing or incorrect token" }; },
  });
  await assert.rejects(apiServices.listDocument(), (err) => {
    assert.equal(err, 401);
    return true;
  });
  assert.equal(jsonCalls, 0, "the 401 error body must not be parsed as documents");
  assert.equal(reloads, 1, "handleError reloads on 401");
});

test("listDocument resolves the parsed body on 200", async () => {
  const body = { Trash: [], Entries: [{ id: "root", name: "My Files" }] };
  stubFetch({
    ok: true,
    status: 200,
    headers: { get: () => "application/json" },
    json: async () => body,
  });
  assert.deepEqual(await apiServices.listDocument(), body);
  assert.equal(reloads, 0);
});

// GlitchTip #185: automation() resolved undefined on 401, so the Automation
// page's poller did setLog(undefined) and crashed reading log.moves.
test("getAutomationLog rejects on 401 instead of resolving undefined", async () => {
  stubFetch({
    ok: false,
    status: 401,
    headers: { get: () => "application/json" },
    json: async () => { jsonCalls += 1; return { error: "missing or incorrect token" }; },
  });
  await assert.rejects(apiServices.getAutomationLog(), (err) => {
    assert.equal(err.status, 401);
    return true;
  });
  assert.equal(jsonCalls, 0);
  assert.equal(reloads, 1, "automation() reloads on 401");
});

test("getAutomationLog resolves the parsed body on 200", async () => {
  const body = { moves: [], runs: [] };
  stubFetch({ ok: true, status: 200, json: async () => body });
  assert.deepEqual(await apiServices.getAutomationLog(), body);
});

test("getCode rejects on 401 without reading the error body", async () => {
  stubFetch({
    ok: false,
    status: 401,
    headers: { get: () => "application/json" },
    json: async () => { jsonCalls += 1; return { error: "missing or incorrect token" }; },
  });
  await assert.rejects(apiServices.getCode(), (err) => {
    assert.equal(err, 401);
    return true;
  });
  assert.equal(jsonCalls, 0);
});
