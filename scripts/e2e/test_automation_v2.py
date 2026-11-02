"""Automation rules v2: the old API answers 410, and two-condition rules (document and
page) are built in the editor, previewed, saved, reloaded and run. Every UI test runs on
desktop and phone (390 px). run.sh turns the rm-pages stub on, so page watch is enabled.
"""
import re

import pytest
from playwright.sync_api import expect

from test_autoorganize_ui import add_rule, assert_healthy, card, new_rule, open_page, pick_source, save_rule
from test_pipeline_ui import ROOT, fits_viewport


def summary(drawer):
    return drawer.get_by_label("Summary").locator(".ao-sentence")


@pytest.mark.parametrize("path", ["/ui/api/auto-organize", "/ui/api/auto-organize/pipeline", "/ui/api/auto-organize/state"])
def test_old_api_answers_410(api, path):
    for method in (api.get, api.put):
        r = method(path)
        assert r.status == 410, (method.__name__, path, r.status)
        body = r.json()
        assert body["error"] == "this API moved to /ui/api/automation", body
        assert body["location"].startswith("/ui/api/automation"), body


def test_two_condition_document_rule(page, api):
    open_page(page)
    drawer = new_rule(page)
    card(drawer, "It sits unopened").click()
    expect(drawer.get_by_label("days", exact=True)).to_have_value("30")
    drawer.get_by_label("days", exact=True).fill("1")
    pick_source(page, drawer, "Inbox")
    # "It sits unopened" adds "at least 7 days old" by itself: drop it so the order is kind, then age
    expect(drawer.get_by_label("condition 1 days")).to_have_value("7")
    drawer.get_by_role("button", name="Remove condition “at least 7 days old”").click()
    drawer.get_by_role("button", name="+ add a condition").click()
    drawer.get_by_role("button", name="only PDFs & books").click()
    drawer.get_by_role("button", name="+ add a condition").click()
    drawer.get_by_role("button", name="at least N days old").click()
    # 0 days, so the seeded PDFs match at once
    drawer.get_by_label("condition 2 days").fill("0")
    expect(summary(drawer)).to_contain_text("AND at least 0 days old")
    # a move needs a destination
    drawer.get_by_role("listbox", name="destination folder").get_by_role("option").filter(
        has_text=re.compile(r"^\W*Archive$")).click()
    sentence = summary(drawer).inner_text()
    assert sentence.endswith("AND at least 0 days old THEN move it to Archive"), sentence
    matches = drawer.get_by_text("Matches now:")
    expect(matches).not_to_contain_text("0 documents")
    expect(matches).to_contain_text(re.compile(r"[12] documents?"))
    fits_viewport(page)
    save_rule(page, drawer)
    row = page.locator(".ao-rule .ao-sentence")
    expect(row).to_have_text(sentence)
    page.reload()
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(sentence)
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["when"]["subject"] == "document", r
    assert [c["type"] for c in r["conditions"]] == ["kind_is", "min_age_days"], r
    assert r["conditions"][0]["kind"] == "pdf_epub" and r["conditions"][1]["days"] == 0, r
    assert r["when"]["trigger"]["type"] == "not_opened_for_days" and r["when"]["trigger"]["days"] == 1, r
    assert [a["type"] for a in r["actions"]] == ["move"], r
    # the saved rule's preview lists the seeded PDF
    page.get_by_role("button", name="Preview", exact=True).click()
    expect(page.get_by_text("would move")).to_be_visible()
    expect(page.locator(".ao-row .doc", has_text="Paper")).to_be_visible()
    # run now (dry mode): succeeds and plans the moves
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text(re.compile(r"Dry run: \d+ would move"))).to_be_visible()
    assert_healthy(page)


def test_two_condition_page_rule(page, api):
    if not api.get("/ui/api/automation/pipeline").json()["enabled"]:
        pytest.skip("page watch is off: run.sh without E2E_STUB_RMPAGES=0")
    open_page(page)
    drawer = new_rule(page)
    card(drawer, "I write on a page").click()
    expect(drawer.locator(".ao-any")).to_have_text("anywhere")
    drawer.get_by_role("button", name="+ add a condition").click()
    drawer.get_by_role("button", name="only notebooks").click()
    drawer.get_by_role("button", name="+ add a condition").click()
    drawer.get_by_role("button", name="named like …").click()
    drawer.get_by_label(re.compile(r"condition \d pattern")).fill("*Notes")
    expect(summary(drawer)).to_have_text(
        "IF I write on a page anywhere (only notebooks) AND named like “*Notes” THEN extract tasks to my inbox")
    # the seeded notebook "E2E Notes" has pages and the glob is anchored, so it matches
    expect(drawer.get_by_text("Matches now:")).to_contain_text(re.compile(r"[1-9]\d* pages? in [1-9]\d* documents?"))
    # a pattern that matches no notebook shows none
    drawer.get_by_label(re.compile(r"condition \d pattern")).fill("Nope*")
    expect(drawer.get_by_text("Matches now:")).to_contain_text("0 pages in 0 documents")
    drawer.get_by_label(re.compile(r"condition \d pattern")).fill("*Notes")
    expect(drawer.get_by_text("Matches now:")).to_contain_text(re.compile(r"[1-9]\d* pages? in"))
    # no way to add a second action: the THEN cards are single-choice
    assert page.get_by_role("button", name=re.compile("add an action", re.I)).count() == 0
    expect(drawer.get_by_role("radiogroup", name="Action").get_by_role("radio")).to_have_count(3)
    fits_viewport(page)
    save_rule(page, drawer)
    sentence = page.locator(".ao-rule .ao-sentence")
    want = "IF I write on a page anywhere (only notebooks) AND named like “*Notes” THEN extract tasks to my inbox"
    expect(sentence).to_have_text(want)
    page.reload()
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(want)
    assert page.get_by_role("button", name=re.compile("add an action", re.I)).count() == 0
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["when"]["subject"] == "page" and r["when"]["in"] == ROOT, r
    assert r["when"]["trigger"]["type"] == "page_changed", r
    assert [c["type"] for c in r["conditions"]] == ["kind_is", "name_matches"], r
    assert r["conditions"][1]["pattern"] == "*Notes", r
    assert len(r["actions"]) == 1 and r["actions"][0]["type"] == "extract_tasks", r["actions"]
    # run now on the page watch
    runs = len(api.get("/ui/api/automation/pipeline").json()["runs"])
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text("Pages checked:")).to_be_visible()
    assert len(api.get("/ui/api/automation/pipeline").json()["runs"]) == runs + 1
    assert_healthy(page)


def test_show_preview_from_unsaved_rule(page):
    """The editor's "▸ show" on a rule that is not saved: the drawer closes and the
    preview tab lists what the draft would move."""
    open_page(page)
    drawer = add_rule(page)
    matches = drawer.get_by_text("Matches now:")
    expect(matches).to_contain_text("1 document")
    drawer.get_by_role("button", name="▸ show").click()
    expect(page.get_by_label("Edit rule")).not_to_be_visible()
    expect(page.get_by_text("would move")).to_be_visible()
    expect(page.locator(".ao-row .doc", has_text="Paper")).to_be_visible()
    # nothing was saved
    assert page.locator(".ao-rule").count() == 0
    assert_healthy(page)
