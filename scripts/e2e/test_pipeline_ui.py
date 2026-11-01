"""Pipeline rules in the Auto-organize UI; every test runs on desktop and phone.

test_pipeline_panel_run_now_shows_a_run needs page watch on in the container:
run with E2E_STUB_RMPAGES=1 (run.sh starts a stub rm-pages and sets RM_PAGES_URL).
"""
from playwright.sync_api import expect
import pytest

from test_autoorganize_ui import add_rule, assert_healthy, open_page, save_rule

SEND = "Send to pipeline"


def start_pipeline_rule(page):
    page.get_by_role("button", name="+ Add rule").click()
    drawer = page.get_by_label("Edit rule")
    expect(drawer).to_be_visible()
    drawer.get_by_role("radio", name=SEND).check()
    return drawer


def watch(drawer, path):
    drawer.get_by_role("listbox", name="watch items").get_by_role("option", name=path, exact=True).click()


def fits_viewport(page):
    w = page.evaluate("[document.documentElement.scrollWidth, window.innerWidth]")
    assert w[0] <= w[1], f"horizontal overflow {w}"


def test_create_pipeline_rule_with_folder_and_document(page, api):
    open_page(page)
    drawer = start_pipeline_rule(page)
    # a pipeline rule has no source, destination or scope
    expect(drawer.get_by_role("listbox", name="source folder")).to_have_count(0)
    expect(drawer.get_by_role("listbox", name="destination folder")).to_have_count(0)
    watch(drawer, "Inbox")
    watch(drawer, "Projects / E2E Notes")
    expect(drawer.get_by_text("Watching 2 items")).to_be_visible()
    fits_viewport(page)
    save_rule(page, drawer)
    expect(page.get_by_text("Watching 2 items → pipeline")).to_be_visible()
    expect(page.get_by_text("a watched page changes")).to_be_visible()
    rules = api.get("/ui/api/auto-organize").json()["rules"]
    assert len(rules) == 1
    r = rules[0]
    assert r["action"]["type"] == "pipeline" and r["trigger"]["type"] == "page_changed"
    assert sorted((w["kind"], w["name"]) for w in r["watch"]) == [("document", "E2E Notes"), ("folder", "Inbox")]
    fits_viewport(page)
    assert_healthy(page)


def test_pipeline_rule_requires_a_watched_item(page, api):
    open_page(page)
    drawer = start_pipeline_rule(page)
    expect(drawer.get_by_text("Pick at least one folder or notebook to watch")).to_be_visible()
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_disabled()
    watch(drawer, "Inbox")
    expect(drawer.get_by_text("Pick at least one folder or notebook to watch")).to_have_count(0)
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_enabled()
    # removing the last item brings the error back
    drawer.get_by_role("button", name="Stop watching Inbox").click()
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_disabled()
    assert api.get("/ui/api/auto-organize").json()["rules"] == []
    assert_healthy(page)


def test_prompt_is_prefilled_and_editable(page, api):
    defaults = api.get("/ui/api/auto-organize/pipeline").json()["defaults"]
    assert "BEFORE" in defaults["prompt"] and defaults["model"]
    open_page(page)
    drawer = start_pipeline_rule(page)
    prompt = drawer.get_by_label("prompt")
    expect(prompt).to_have_value(defaults["prompt"])
    expect(drawer.get_by_label("model")).to_have_value(defaults["model"])
    prompt.fill("Only list tasks for Alice.")
    drawer.get_by_label("model").fill("some/model")
    watch(drawer, "Archive")
    save_rule(page, drawer)
    open_page(page)
    page.get_by_label("Rule actions").first.click()
    page.get_by_role("button", name="Edit", exact=True).click()
    drawer = page.get_by_label("Edit rule")
    expect(drawer.get_by_label("prompt")).to_have_value("Only list tasks for Alice.")
    expect(drawer.get_by_label("model")).to_have_value("some/model")
    r = api.get("/ui/api/auto-organize").json()["rules"][0]
    assert r["action"]["prompt"] == "Only list tasks for Alice."
    assert_healthy(page)


def test_pipeline_panel_run_now_shows_a_run(page, api):
    if not api.get("/ui/api/auto-organize/pipeline").json()["enabled"]:
        pytest.skip("page watch is off: run with E2E_STUB_RMPAGES=1")
    open_page(page)
    panel = page.locator(".ao-pipeline")
    expect(panel.get_by_role("heading", name="Pipeline")).to_be_visible()
    before = len(api.get("/ui/api/auto-organize/pipeline").json()["runs"])
    panel.get_by_role("button", name="Run now").click()
    expect(panel.locator(".ao-prun").filter(has_text="run now").first).to_be_visible()
    assert len(api.get("/ui/api/auto-organize/pipeline").json()["runs"]) == before + 1
    fits_viewport(page)
    assert_healthy(page)


def test_move_rules_still_work(page, api):
    open_page(page)
    drawer = add_rule(page)
    # Move is the default action
    expect(drawer.get_by_role("radio", name="Move")).to_be_checked()
    save_rule(page, drawer)
    expect(page.locator(".ao-sent .p")).to_contain_text("name matches")
    rules = api.get("/ui/api/auto-organize").json()["rules"]
    assert rules[0]["action"]["type"] == "move"
    assert_healthy(page)
