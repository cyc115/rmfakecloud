"""The Automation tab (Option A): rename + redirect, the IF/THEN cards, the
condition, the summary sentence and the list. Every test runs on desktop and
phone (390 px). run.sh seeds the page tag "todo" on E2E Notes p1.
"""
import os
import re

import pytest
from playwright.sync_api import expect

from test_autoorganize_ui import add_rule, assert_healthy, card, new_rule, open_page, pick, pick_source, save_rule
from test_pipeline_ui import ROOT, fits_viewport, start_pipeline_rule, watch


def summary(drawer):
    return drawer.get_by_label("Summary").locator(".ao-sentence")


def test_rename_and_redirect(page):
    page.goto("/auto-organize")
    expect(page).to_have_url(re.compile(r"/automation$"))
    expect(page.get_by_role("heading", name="Automation", exact=True)).to_be_visible()
    nav = page.locator("nav a[href='/automation']")
    expect(nav).to_have_count(1)
    expect(nav).to_have_text("Automation")
    assert "Auto-organize" not in page.inner_text("body")
    fits_viewport(page)
    assert_healthy(page)


def test_card_catalogue_matches_the_mock(page):
    open_page(page)
    drawer = new_rule(page)
    expect(drawer.get_by_role("heading", name="When this happens…")).to_be_visible()
    expect(drawer.get_by_role("heading", name="…do this")).to_be_visible()
    page_cards = drawer.get_by_role("radiogroup", name="Trigger: On a page").get_by_role("radio")
    expect(page_cards).to_have_count(2)
    doc_cards = drawer.get_by_role("radiogroup", name="Trigger: On a document").get_by_role("radio")
    expect(doc_cards).to_have_count(10)
    for title in ("I finish reading", "I've read X%"):
        expect(card(drawer, title)).to_contain_text("PDFs & books")
    expect(drawer.get_by_role("radiogroup", name="Action").get_by_role("radio")).to_have_count(5)
    fits_viewport(page)
    assert_healthy(page)


def test_page_tagged_just_save_notes(page, api):
    open_page(page)
    drawer = start_pipeline_rule(page)
    card(drawer, "I tag a page, or the whole document").click()
    expect(drawer.get_by_text("Pick or type a page tag.")).to_be_visible()
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_disabled()
    expect(drawer.get_by_text("your tags: todo")).to_be_visible()  # suggested from GET page-tags
    drawer.get_by_label("page tag").fill("To-Do")  # normalises to "to do": not the seeded "todo"
    expect(drawer.get_by_text("0 pages are tagged “to do” now")).to_be_visible()
    drawer.get_by_label("page tag").fill("todo")
    expect(drawer.get_by_text("1 page is tagged “todo” now")).to_be_visible()
    card(drawer, "Save notes & annotations").click()
    expect(card(drawer, "Save notes & annotations")).to_have_attribute("aria-checked", "true")
    expect(summary(drawer)).to_have_text("IF I tag a page “todo” anywhere THEN save notes & annotations")
    expect(drawer.get_by_text("Applies from your next change")).to_be_visible()
    # the In places narrow the count: Inbox holds no tagged page
    watch(drawer, "Inbox")
    expect(drawer.get_by_text("0 pages are tagged “todo” there now")).to_be_visible()
    drawer.get_by_role("button", name="Stop watching Inbox").click()
    fits_viewport(page)
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text("IF I tag a page “todo” anywhere THEN save notes & annotations")
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["when"]["subject"] == "page", r
    assert r["when"]["trigger"]["type"] == "page_tagged" and r["when"]["trigger"]["tag"] == "todo", r
    assert [a["type"] for a in r["actions"]] == ["save_notes"], r
    assert r["when"]["in"] == ROOT, r["when"]["in"]
    assert_healthy(page)


def test_invalid_actions_are_disabled_with_the_reason(page):
    open_page(page)
    drawer = new_rule(page)
    move = card(drawer, "Move it to…")
    expect(move).to_be_disabled()
    # one callout for the three document actions
    expect(drawer.locator(".ao-callout")).to_have_count(1)
    expect(drawer.locator(".ao-callout")).to_contain_text("Move it to…, Add a tag, Remove a tag are not offered here")
    expect(drawer.locator(".ao-callout")).to_contain_text("Moving and tagging work on whole documents")
    card(drawer, "It sits unopened").click()
    expect(move).to_have_attribute("aria-checked", "true")
    for title in ("Extract tasks & annotations", "Save notes & annotations"):
        expect(card(drawer, title)).to_be_disabled()
    expect(drawer.locator(".ao-callout")).to_have_count(1)
    expect(drawer.locator(".ao-callout")).to_contain_text("Extract tasks & annotations, Save notes & annotations are not offered here. Works on pages")
    expect(drawer.get_by_text("Move rules work on the documents directly in one folder.")).to_be_visible()
    fits_viewport(page)
    assert_healthy(page)


def test_pdf_only_kind_is_is_locked_and_other_conditions_can_be_added(page, api):
    open_page(page)
    drawer = new_rule(page)
    card(drawer, "I finish reading").click()
    expect(drawer.get_by_text("Fixed: “I finish reading” uses the page you stopped on, which only PDFs & books report.")).to_be_visible()
    # only kind_is is locked: the other conditions are still offered
    drawer.get_by_role("button", name="+ add a condition").click()
    expect(drawer.get_by_role("button", name="only notebooks")).to_have_count(0)
    expect(drawer.get_by_role("button", name="only PDFs & books")).to_have_count(0)
    expect(drawer.get_by_role("button", name="named like …")).to_have_count(1)
    drawer.get_by_role("button", name="cancel", exact=True).click()
    card(drawer, "Its name matches").click()
    drawer.get_by_role("button", name="+ add a condition").click()
    drawer.get_by_role("button", name="only notebooks").click()
    pick_source(page, drawer, "Inbox")
    drawer.get_by_label("pattern").fill("Paper*")
    pick(page, "destination folder", "Archive")
    expect(summary(drawer)).to_have_text("IF a document named like “Paper*” is in Inbox (only notebooks) THEN move it to Archive")
    expect(drawer.get_by_text("Matches now:")).to_contain_text("0 documents")  # Paper is a PDF
    drawer.get_by_role("button", name="Remove condition").click()
    expect(drawer.get_by_text("Matches now:")).to_contain_text("1 document")
    save_rule(page, drawer)
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["when"]["subject"] == "document" and [a["type"] for a in r["actions"]] == ["move"], r
    assert r["conditions"] == [], r  # kind_is was removed; the pattern is the trigger
    assert_healthy(page)


def test_save_reload_edit_rename_delete(page, api):
    open_page(page)
    drawer = add_rule(page)
    expect(drawer.get_by_text("Name: auto from the sentence")).to_be_visible()
    save_rule(page, drawer)
    page.reload()
    rule = page.locator(".ao-rule")
    expect(rule.locator(".ao-sentence")).to_have_text("IF a document named like “Paper*” is in Inbox THEN move it to Archive")
    expect(rule.locator(".ao-meta")).to_contain_text("1 would move now")
    rule.locator(".ao-sentence").click()
    drawer = page.get_by_label("Edit rule")
    expect(drawer.get_by_role("heading", name="Edit automation")).to_be_visible()
    drawer.get_by_label("pattern").fill("Multi*")
    drawer.get_by_role("button", name="rename").click()
    drawer.get_by_label("rule name").fill("file the big one")
    save_rule(page, drawer)
    expect(rule.locator(".ao-rname")).to_have_text("file the big one")
    expect(rule.locator(".ao-sentence")).to_have_text("IF a document named like “Multi*” is in Inbox THEN move it to Archive")
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["name"] == "file the big one" and r["when"]["trigger"]["pattern"] == "Multi*", r
    rule.locator(".ao-sentence").click()
    page.once("dialog", lambda d: d.accept())
    page.get_by_label("Edit rule").get_by_role("button", name="Delete", exact=True).click()
    expect(page.get_by_text("No automations yet")).to_be_visible()
    assert api.get("/ui/api/automation").json()["rules"] == []
    assert_healthy(page)


def test_server_error_is_shown_inline(page, api):
    open_page(page)
    # a lookbehind is valid in JavaScript but not in RE2: only the server refuses it
    drawer = add_rule(page, pattern="/(?<=a)b/")
    drawer.get_by_role("button", name="Save", exact=True).click()
    expect(drawer.get_by_role("alert")).to_contain_text("invalid regular expression")
    expect(drawer).to_be_visible()
    assert api.get("/ui/api/automation").json()["rules"] == []
    # the browser logs the 400 response itself; nothing else may be logged
    page.console_errors[:] = [e for e in page.console_errors if "status of 400" not in e]
    assert_healthy(page)


INBOX = {"id": "11111111-1111-4111-8111-111111111111", "kind": "folder", "name": "Inbox"}
LIVE_LIKE = [
    {  # Mike's move rule, on the seeded folders (no min_age_days: the plan's ruling P1)
        "id": "", "name": "", "enabled": True,
        "when": {"subject": "document", "in": [INBOX],
                 "trigger": {"type": "finished_reading", "minPages": 2, "settleHours": 6}},
        "conditions": [{"type": "kind_is", "kind": "pdf_epub"}],
        "actions": [{"type": "move", "to": {"id": "", "name": "", "createIfMissing": {"name": "Read", "parent": "source"}}}],
    },
    {  # Mike's page rule: a custom name, two watched documents, a custom prompt
        "id": "", "name": "capture journal pages", "enabled": True,
        "when": {"subject": "page", "trigger": {"type": "page_changed"},
                 "in": [{"id": "55555555-5555-4555-8555-555555555555", "kind": "document", "name": "E2E Notes"}]},
        "conditions": [],
        "actions": [{"type": "extract_tasks", "prompt": "Custom prompt.", "model": "deepseek/deepseek-v4.1-flash"}],
    },
]


def put_rules(api, rules):
    cfg = api.get("/ui/api/automation").json()
    cfg["rules"] = rules
    r = api.put("/ui/api/automation", data=cfg)
    assert r.ok, r.text()


def put_live_like(api):
    from conftest import find
    docs = api.get("/ui/api/documents").json()["Entries"]
    paper = find(docs, "Paper")
    rules = [dict(r) for r in LIVE_LIKE]
    page_when = rules[1]["when"]
    rules[1] = {**rules[1], "when": {**page_when, "in": page_when["in"] + [{"id": paper["id"], "kind": "document", "name": "Paper"}]}}
    put_rules(api, rules)


def test_live_like_rules_render_as_sentences(page, api):
    put_live_like(api)
    open_page(page)
    sentences = page.locator(".ao-rule .ao-sentence")
    expect(sentences).to_have_text([
        "IF I finish reading a document in Inbox THEN move it to Read",
        "IF I write on a page in E2E Notes or Paper THEN extract tasks & annotations",
    ])
    expect(page.locator(".ao-rule").nth(1).locator(".ao-rname")).to_have_text("capture journal pages")
    # editing the page rule shows its parts; Cancel leaves it untouched
    sentences.nth(1).click()
    drawer = page.get_by_label("Edit rule")
    expect(card(drawer, "I write on a page")).to_have_attribute("aria-checked", "true")
    expect(drawer.get_by_role("button", name=re.compile("Stop watching .*Paper"))).to_be_visible()
    drawer.get_by_text("Customize what to extract (prompt)").click()
    expect(drawer.get_by_label("prompt")).to_have_value("Custom prompt.")
    drawer.get_by_role("button", name="Cancel", exact=True).click()
    fits_viewport(page)
    assert_healthy(page)


def unroll(page):
    """Full-page shots: drop the toasts, the hover, and the app's inner scroll containers."""
    page.evaluate("document.querySelectorAll('.Toastify__toast').forEach(e => e.remove())")
    page.mouse.move(1, 1)
    page.add_style_tag(content=".ao, div[style*='overflow: auto'], div[style*='height: 100%']"
                       "{overflow: visible !important; height: auto !important}")
    page.wait_for_timeout(300)


def test_screenshots(page, api, request):
    """E2E_SHOTS=<dir>: list and editor screenshots for the report (desktop and phone)."""
    out = os.environ.get("E2E_SHOTS")
    if not out:
        pytest.skip("set E2E_SHOTS to save screenshots")
    os.makedirs(out, exist_ok=True)
    kind = "phone" if page.viewport_size["width"] < 500 else "desktop"
    put_live_like(api)
    cfg = api.get("/ui/api/automation").json()
    cfg["rules"].append({
        "id": "", "name": "", "enabled": True,
        "when": {"subject": "page", "in": [{"id": "", "kind": "folder", "name": "My files"}], "trigger": {"type": "page_tagged", "tag": "todo"}},
        "conditions": [], "actions": [{"type": "save_notes"}],
    })
    cfg["rules"].append({
        "id": "", "name": "", "enabled": False,
        "when": {"subject": "document", "in": [INBOX], "trigger": {"type": "not_opened_for_days", "days": 30}},
        "conditions": [{"type": "min_age_days", "days": 7}],
        "actions": [{"type": "move", "to": {"id": "22222222-2222-4222-8222-222222222222", "name": "Archive"}}],
    })
    assert api.put("/ui/api/automation", data=cfg).ok
    open_page(page)
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text("Dry run:")).to_be_visible()
    unroll(page)
    page.screenshot(path=f"{out}/list-{kind}.png", full_page=True)
    drawer = start_pipeline_rule(page)
    card(drawer, "I tag a page, or the whole document").click()
    drawer.get_by_label("page tag").fill("todo")
    watch(drawer, "Projects")
    drawer.get_by_role("button", name="done", exact=True).click()
    expect(drawer.get_by_text("1 page is tagged “todo” there now")).to_be_visible()
    # full editor: unroll the drawer's scroll container for the shot
    page.add_style_tag(content=".ao-drawer{position:absolute!important;bottom:auto!important}.ao-db{overflow:visible!important}")
    unroll(page)
    page.screenshot(path=f"{out}/editor-{kind}.png", full_page=True)
    assert_healthy(page)


def test_no_edit_save_keeps_the_stored_rule(page, api):
    put_live_like(api)
    before = api.get("/ui/api/automation").json()["rules"]
    open_page(page)
    for i in range(2):
        page.locator(".ao-rule .ao-sentence").nth(i).click()
        save_rule(page, page.get_by_label("Edit rule"))
    after = api.get("/ui/api/automation").json()["rules"]
    for b, a in zip(before, after):
        for k in ("name", "when", "conditions", "actions"):
            assert a.get(k) == b.get(k), (k, b.get(k), a.get(k))
    assert after[0].get("name", "") == ""  # auto-named stays "" (omitted), shown as its sentence
    assert_healthy(page)


def test_page_tags_load_only_for_page_tagged_and_failures_offer_retry(page):
    calls = []
    page.on("request", lambda r: calls.append(r.url) if "/page-tags" in r.url else None)
    page.route("**/automation/page-tags", lambda route: route.abort())
    open_page(page)
    drawer = start_pipeline_rule(page)
    card(drawer, "Its name matches").click()
    page.wait_for_timeout(300)
    assert calls == [], "page tags fetched without page_tagged"
    card(drawer, "I tag a page, or the whole document").click()
    expect(drawer.get_by_text("Couldn't load your page tags")).to_be_visible()
    page.unroute("**/automation/page-tags")
    drawer.get_by_role("button", name="(retry)").click()
    expect(drawer.get_by_text("your tags: todo")).to_be_visible()
    page.console_errors[:] = [e for e in page.console_errors if "ERR_FAILED" not in e]
    assert_healthy(page)


def test_watchables_failure_offers_retry(page):
    page.route("**/automation/watchables", lambda route: route.abort())
    open_page(page)
    drawer = start_pipeline_rule(page)
    expect(drawer.get_by_text("Couldn't load your folders and notebooks")).to_be_visible()
    page.unroute("**/automation/watchables")
    drawer.get_by_role("button", name="(retry)").click()
    expect(drawer.get_by_text("Couldn't load your folders and notebooks")).to_have_count(0)
    watch(drawer, "Inbox")
    expect(drawer.get_by_role("button", name="Stop watching Inbox")).to_be_visible()
    if page.viewport_size["width"] < 500:
        box = drawer.get_by_role("button", name="Stop watching Inbox").bounding_box()
        assert box["width"] >= 32 and box["height"] >= 32, box
    page.console_errors[:] = [e for e in page.console_errors if "ERR_FAILED" not in e]
    assert_healthy(page)


def test_apply_runs_moves_only_and_undo_from_the_status_line(page, api):
    if not api.get("/ui/api/automation/pipeline").json()["enabled"]:
        pytest.skip("page watch is off: run.sh without E2E_STUB_RMPAGES=0")
    open_page(page)
    save_rule(page, start_pipeline_rule(page))  # a page rule: Apply must not check pages
    save_rule(page, add_rule(page))
    page.locator(".ao-seg label", has_text="Live").click()
    expect(page.locator(".ao-seg label.on")).to_have_text("Live")
    runs = len(api.get("/ui/api/automation/pipeline").json()["runs"])
    page.get_by_role("button", name="Preview", exact=True).click()
    page.once("dialog", lambda d: d.accept())
    page.get_by_role("button", name="Apply this change").click()
    expect(page.get_by_text("Documents checked: 1 moved")).to_be_visible()
    assert len(api.get("/ui/api/automation/pipeline").json()["runs"]) == runs
    move_rule = page.locator(".ao-rule").nth(1)
    expect(move_rule.locator(".ao-meta")).to_contain_text("moved Paper")
    move_rule.get_by_role("button", name="undo").click()
    expect(page.get_by_text("back to Inbox")).to_be_visible()
    from test_autoorganize_ui import inbox_docs
    assert "Paper" in inbox_docs(api)
    assert_healthy(page)
