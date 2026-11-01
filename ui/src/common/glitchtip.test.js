// Run: cd ui && npm test
import { test } from "node:test";
import assert from "node:assert/strict";
import { envelopeUrl, parseStack, buildEvent, envelope } from "./glitchtip.js";

test("DSN becomes an envelope URL with the key in the query string", () => {
  assert.equal(
    envelopeUrl("http://abc123@log.svc/24"),
    "http://log.svc/api/24/envelope/?sentry_key=abc123&sentry_version=7",
  );
  assert.equal(envelopeUrl(""), null);
  assert.equal(envelopeUrl(undefined), null);
  assert.equal(envelopeUrl("not a dsn"), null);
});

test("Firefox stack: oldest frame first, vendor chunks not in-app", () => {
  const frames = parseStack(
    "Rh@http://remarkable.svc/assets/index-Ct20iq1r.js:1:52011\n" +
    "Kc@http://remarkable.svc/assets/react-D-GVefo5.js:8:12\n",
  );
  assert.deepEqual(frames.map((f) => [f.function, f.lineno, f.colno, f.in_app]), [
    ["Kc", 8, 12, false],
    ["Rh", 1, 52011, true],
  ]);
});

test("Chrome stack, with and without a function name", () => {
  const frames = parseStack(
    "TypeError: x\n" +
    "    at Rh (http://remarkable.svc/assets/index-a.js:1:5)\n" +
    "    at http://remarkable.svc/assets/index-a.js:2:6",
  );
  assert.deepEqual(frames.map((f) => [f.function, f.filename, f.lineno]), [
    ["?", "http://remarkable.svc/assets/index-a.js", 2],
    ["Rh", "http://remarkable.svc/assets/index-a.js", 1],
  ]);
});

test("event carries the error, page, release and navigation trail", () => {
  const err = new TypeError("can't access property \"filter\", s is undefined");
  const ev = buildEvent(err, {
    release: "0.0.31",
    url: "http://remarkable.svc/documents/b",
    userAgent: "Firefox",
    trail: [{ timestamp: 1, path: "/documents/f" }, { timestamp: 2, path: "/documents/b" }],
    extra: { componentStack: "at Folder" },
  });
  assert.match(ev.event_id, /^[0-9a-f]{32}$/);
  assert.equal(ev.release, "0.0.31");
  assert.deepEqual(ev.exception.values[0].type, "TypeError");
  assert.match(ev.exception.values[0].value, /s is undefined/);
  assert.deepEqual(ev.breadcrumbs.values.map((b) => b.message), ["/documents/f", "/documents/b"]);
  assert.equal(ev.extra.componentStack, "at Folder");
});

test("non-Error values (rejected strings) still report", () => {
  const ev = buildEvent("boom");
  assert.equal(ev.exception.values[0].value, "boom");
});

test("envelope = header, item header, event, one JSON per line", () => {
  const ev = buildEvent(new Error("x"));
  const lines = envelope(ev).split("\n").map((l) => JSON.parse(l));
  assert.equal(lines.length, 3);
  assert.equal(lines[0].event_id, ev.event_id);
  assert.equal(lines[1].type, "event");
  assert.equal(lines[2].exception.values[0].value, "x");
});

test("works without crypto.randomUUID (plain-http pages are not secure contexts)", () => {
  const orig = crypto.randomUUID;
  Object.defineProperty(crypto, "randomUUID", { value: undefined, configurable: true });
  try {
    assert.match(buildEvent(new Error("x")).event_id, /^[0-9a-f]{32}$/);
  } finally {
    Object.defineProperty(crypto, "randomUUID", { value: orig, configurable: true });
  }
});
