"""End to end: a handwritten page change becomes a vault note section and a staged task.

Needs the REAL rm-pages (it calls the LLM, ~$0.001 a run) and the homelab vault writer:

    E2E_STUB_RMPAGES=0 RM_PAGES_URL=http://host.containers.internal:8801 PAGEWATCH_MIN_AGE=1ms \\
      E2E_WRITER=~/workspace/github/homelab/scripts/rm-pages-vault/writer.py \\
      scripts/e2e/run.sh -k pipeline_e2e

A real run (RM_PAGES_URL set, no stub) with E2E_WRITER missing fails rather than skips.

rm-pages must read user e2e's blobs from the e2e data dir (RMPAGES_EXTRA_ROOTS=e2e=...,
set by homelab's ansible/rm-pages.yml). run.sh deletes every rm-pages row of user e2e
before and after the run. The vault is a temp dir; the live timeline is only read (plus
one sync round) to prove e2e events never reach it.

Flow (R12/R15): p1 becomes a legible note page (text_note.rm); the rule's first run makes
"E2E Notes" a member through transcribe_only (p1); p2 is seeded after that run, so the next run sends it as an edit with no before
page, and its tasks are extracted.
"""
import datetime
import json
import os
import shlex
import subprocess
import time
import urllib.parse
import urllib.request
from pathlib import Path

import pytest
from playwright.sync_api import expect

from conftest import _context
from test_autoorganize_ui import assert_healthy, open_page, save_rule
from test_pipeline_ui import open_prompt, start_pipeline_rule, watch

RM_PAGES_URL = os.environ.get("RM_PAGES_URL", "")
API = os.environ.get("E2E_RMPAGES_API", "http://10.0.0.11:8801").rstrip("/")  # reachable from here
TIMELINE = os.environ.get("E2E_TIMELINE_URL", "http://10.0.0.11:8800").rstrip("/")
WRITER = os.path.expanduser(os.environ.get(
    "E2E_WRITER", "~/workspace/github/homelab/scripts/rm-pages-vault/writer.py"))
HOST = os.environ.get("E2E_HOST", "mike@10.0.0.11")
REMOTE = os.environ.get("E2E_REMOTE", "/tmp/rmfc-e2e")
DATA = os.environ.get("E2E_DATA", "/home/mike/rmfc-e2e-data")
GO_IMG = "docker.io/library/golang:bookworm"
TESTDATA = "/src/internal/testkit/tablet/testdata"  # in the rmseed container
USER = "e2e"
DOC = "55555555-5555-4555-8555-555555555555"  # "E2E Notes" in Projects, seeded by run.sh

REAL_RUN = bool(RM_PAGES_URL) and os.environ.get("E2E_STUB_RMPAGES", "0") != "1"

pytestmark = pytest.mark.skipif(
    not REAL_RUN, reason="needs the real rm-pages (RM_PAGES_URL and E2E_STUB_RMPAGES=0)")


@pytest.fixture(autouse=True)
def writer_present():
    """A real run without the vault writer must fail loudly, never skip."""
    if not os.path.isfile(WRITER):
        pytest.fail(f"E2E_WRITER not found: {WRITER} (set E2E_WRITER to homelab's rm-pages-vault/writer.py)")

INBOX = """# Inbox

## Do now
- [ ] Renew the passport #project/personal [captured:: 2026-10-30]

## This week
- [ ] Book the dentist cleaning #project/personal [captured:: 2026-10-31]
"""

PROJECT_TAGS = """# Project Tags

## The vocabulary
- #project/personal — errands, groceries, shopping, household, pets, health
- #project/career — job search, interviews, resume
- #project/finance — accounts, taxes, insurance
- #project/fitness — training, workouts

## Retired tags
- #project/misc — use #project/personal
"""


def get_json(url, timeout=30):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def results(**q):
    q = {"unapplied": "true", "user": USER, "limit": "500", **q}
    return get_json(f"{API}/api/results?{urllib.parse.urlencode(q)}")["results"]


def wait_for(fn, timeout, what, every=3):
    """fn() until truthy; returns its value."""
    end = time.monotonic() + timeout
    while True:
        v = fn()
        if v:
            return v
        if time.monotonic() > end:
            pytest.fail(f"timed out after {timeout}s waiting for {what}")
        time.sleep(every)


def rmseed(*args):
    """cmd/rmseed against the running container's data dir (built by run.sh)."""
    cmd = (f"podman run --rm -v {REMOTE}:/src:Z -v {DATA}:/data:Z -w /src {GO_IMG} "
           f"/src/rmseed --data /data --user {USER} " + " ".join(shlex.quote(a) for a in args))
    subprocess.run(["ssh", HOST, cmd], check=True, timeout=120)


def run_pipeline(page, api):
    """Click the header's Run now (only page rules exist: it checks pages); returns the run entry."""
    before = len(api.get("/ui/api/automation/pipeline").json()["runs"])
    page.locator(".ao-hdr").get_by_role("button", name="Run now").click()
    wait_for(lambda: len(api.get("/ui/api/automation/pipeline").json()["runs"]) > before, 60, "the run", 1)
    run = api.get("/ui/api/automation/pipeline").json()["lastRun"]
    assert run["trigger"] == "run now" and run["mode"] == "live" and not run.get("error"), run
    return run


def page_result(kind, page_id):
    for r in results(watched="true"):
        ev = r["event"]
        if ev["kind"] == kind and ev["page_id"] == page_id and ev["doc_id"] == DOC:
            return r
    return None


@pytest.fixture
def desktop(browser, base_url):
    ctx, pg = _context(browser, base_url)
    yield pg, ctx.request
    ctx.close()


def test_page_change_to_vault_note_and_staged_task(desktop, tmp_path):
    page, api = desktop
    pipe = api.get("/ui/api/automation/pipeline").json()
    assert pipe["enabled"], "page watch is off: RM_PAGES_URL did not reach the container"

    # p1 as seeded is 3 synthetic strokes: unreadable, so its transcription would be blank
    # and rm-pages fails such a page on purpose (R23). Make it a legible page with no tasks.
    rmseed("page", DOC, "p1", TESTDATA + "/text_note.rm")

    # 1. UI: Live mode + a pipeline rule watching Projects with the default prompt
    open_page(page)
    page.locator(".ao-seg label", has_text="Live").click()  # the radio itself is hidden
    expect(page.locator(".ao-seg label.on")).to_have_text("Live")
    drawer = start_pipeline_rule(page)
    watch(drawer, "Projects")
    open_prompt(drawer)
    expect(drawer.get_by_label("prompt")).to_have_value(pipe["defaults"]["prompt"])
    save_rule(page, drawer)
    cfg = api.get("/ui/api/automation").json()
    assert cfg["settings"]["enabled"] and cfg["settings"]["mode"] == "live"
    (rule,) = cfg["rules"]
    assert [a["type"] for a in rule["actions"]] == ["extract_tasks"]
    assert [(w["kind"], w["name"]) for w in rule["when"]["in"]] == [("folder", "Projects")]
    assert rule["actions"][0]["prompt"] == pipe["defaults"]["prompt"]

    # 2. first run: E2E Notes joins the rule, p1 goes as transcribe_only
    first = run_pipeline(page, api)
    assert first["pipeline"] >= 1, first
    p1 = wait_for(lambda: page_result("transcribe_only", "p1"), 240, "p1 transcribe_only result")
    assert "parcel" in p1["transcription"].lower() and p1["tasks"] == [], p1

    # 3. a new page with a typed to-do list -> edit with an empty before page
    rmseed("page", DOC, "p2", TESTDATA + "/text_todo.rm")
    second = run_pipeline(page, api)
    assert second["pipeline"] >= 1, second
    assert_healthy(page)

    # 4. rm-pages: trace done, tasks extracted, "-inbox" line skipped
    p2 = wait_for(lambda: page_result("edit", "p2"), 240, "p2 edit result")
    tid = p2["trace_id"]
    trace = wait_for(lambda: (lambda t: t if t["job"]["status"] in ("done", "failed") else None)(
        get_json(f"{API}/api/trace/{tid}")), 60, f"trace {tid} done")
    print(f"\nE2E p2 trace {tid}: tasks={trace['result']['tasks']} skipped={trace['result']['skipped']}")
    assert trace["job"]["status"] == "done", trace["job"]
    assert trace["event"]["watched"] is True and trace["event"]["rule_id"] == rule["id"]
    assert not trace["event"].get("before_hash")
    assert any("buy oat milk" in t.lower() for t in trace["result"]["tasks"]), trace["result"]
    assert not any("vet" in t.lower() for t in trace["result"]["tasks"]), trace["result"]
    assert any("call the vet" in s.lower() for s in trace["result"]["skipped"]), trace["result"]
    assert {r["stage"] for r in trace["trail"]} >= {"render", "llm"}

    # 5. the vault writer, into a temp vault
    vault, state = tmp_path / "vault", tmp_path / "state.json"
    vault.mkdir()
    (vault / "inbox.md").write_text(INBOX)
    (vault / "project-tags.md").write_text(PROJECT_TAGS)
    out = subprocess.run(
        ["python3", WRITER, "--once", "--user", USER, "--vault", str(vault), "--state", str(state)],
        env={**os.environ, "RM_PAGES_URL": API}, capture_output=True, text=True, timeout=300)
    print("writer:", out.returncode, out.stdout, out.stderr)
    assert out.returncode == 0, out.stderr
    note = (vault / "personal" / "handwritten" / "E2E Notes.md").read_text()
    staged = (vault / "inbox-remarkable.md").read_text()
    print("NOTE:\n" + note + "\nSTAGED:\n" + staged)
    assert "### Page 1" in note and "### Page 2" in note
    assert DOC in note  # frontmatter remarkable_doc_id
    page1, page2 = note.lower().split("### page 1", 1)[1].split("### page 2", 1)
    assert "parcel" in page1 and "oat milk" in page2
    oat = [ln for ln in staged.splitlines() if "oat milk" in ln.lower()]
    assert len(oat) == 1, staged
    assert oat[0].lstrip().startswith("- [ ] ") and "#project/" in oat[0] and "→ [[" in oat[0], oat[0]
    assert "personal/handwritten/E2E Notes#Page 2" in oat[0], oat[0]
    assert "vet" not in staged.lower(), staged
    assert (vault / "inbox.md").read_text() == INBOX  # never edited
    assert list((vault / "attachments" / "remarkable" / DOC).glob("p2-*-after.png"))
    assert results(watched="true") == []  # every watched result acked

    # 6. the live timeline never shows user e2e (TIMELINE_RM_USER=admin), even after a sync round
    ours = {r["trace_id"] for r in results()} | {p1["trace_id"], tid}
    feed = [r for r in results(watched="false") if r["event"]["kind"] in ("open", "edit")]
    assert feed, "no unwatched feed results for e2e: the timeline check would prove nothing"
    req = urllib.request.Request(f"{TIMELINE}/api/sync", data=b"{}", method="POST",
                                 headers={"Content-Type": "application/json"})
    urllib.request.urlopen(req, timeout=300).read()
    now = datetime.datetime.now(datetime.timezone.utc)
    q = urllib.parse.urlencode({"kinds": "remarkable.open,remarkable.edit",
                                "from": (now - datetime.timedelta(hours=24)).isoformat(),
                                "to": (now + datetime.timedelta(hours=1)).isoformat()})
    events = get_json(f"{TIMELINE}/api/events?{q}")
    leaked = [e for e in events if e.get("source_id") in ours
              or (e.get("payload") or {}).get("doc_id") == DOC]
    print(f"timeline: {len(events)} remarkable events today, {len(leaked)} from e2e")
    assert leaked == []
