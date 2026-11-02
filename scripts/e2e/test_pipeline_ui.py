"""Page rules (I write on a page → Extract tasks) in the Automation UI; every test runs
on desktop and phone.

The run-now tests need page watch on in the container: run.sh starts a stub
rm-pages and sets RM_PAGES_URL by default (E2E_STUB_RMPAGES=0 opts out).
"""
from playwright.sync_api import expect
import pytest

from test_autoorganize_ui import add_rule, assert_healthy, card, new_rule, open_page, save_rule

ROOT = [{"id": "", "kind": "folder", "name": "My files"}]


def start_pipeline_rule(page):
    """+ New starts as I write on a page → Extract tasks to my inbox."""
    drawer = new_rule(page)
    expect(card(drawer, "I write on a page")).to_have_attribute("aria-checked", "true")
    expect(card(drawer, "Extract tasks to my inbox")).to_have_attribute("aria-checked", "true")
    return drawer


def watch(drawer, path):
    listbox = drawer.get_by_role("listbox", name="watch items")
    if not listbox.is_visible():
        drawer.get_by_role("button", name="+ add", exact=True).click()
    listbox.get_by_role("option", name=path, exact=True).click()


def open_prompt(drawer):
    drawer.get_by_text("Customize what to extract (prompt)").click()


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
    want = "IF I write on a page in Inbox or E2E Notes THEN extract tasks to my inbox"
    expect(drawer.get_by_label("Summary").locator(".ao-sentence")).to_have_text(want)
    expect(drawer.get_by_text("Applies from your next change")).to_be_visible()
    fits_viewport(page)
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(want)
    rules = api.get("/ui/api/auto-organize").json()["rules"]
    assert len(rules) == 1
    r = rules[0]
    assert r["action"]["type"] == "pipeline" and r["trigger"]["type"] == "page_changed"
    assert sorted((w["kind"], w["name"]) for w in r["watch"]) == [("document", "E2E Notes"), ("folder", "Inbox")]
    assert "tasks" not in r["action"]
    assert r.get("name", "") == ""  # auto-named (omitted when empty): the list shows the sentence
    fits_viewport(page)
    assert_healthy(page)


def test_empty_in_means_anywhere(page, api):
    open_page(page)
    drawer = start_pipeline_rule(page)
    watch(drawer, "Inbox")
    # removing the last place is "anywhere", not an error
    drawer.get_by_role("button", name="Stop watching Inbox").click()
    expect(drawer.locator(".ao-any")).to_have_text("anywhere")
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_enabled()
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text("IF I write on a page anywhere THEN extract tasks to my inbox")
    (r,) = api.get("/ui/api/auto-organize").json()["rules"]
    assert r["watch"] == ROOT, r["watch"]
    assert_healthy(page)


def test_prompt_is_prefilled_and_editable(page, api):
    defaults = api.get("/ui/api/auto-organize/pipeline").json()["defaults"]
    assert "BEFORE" in defaults["prompt"] and defaults["model"]
    open_page(page)
    drawer = start_pipeline_rule(page)
    open_prompt(drawer)
    prompt = drawer.get_by_label("prompt")
    expect(prompt).to_have_value(defaults["prompt"])
    expect(drawer.get_by_label("model")).to_have_value(defaults["model"])
    prompt.fill("Only list tasks for Alice.")
    drawer.get_by_label("model").fill("some/model")
    watch(drawer, "Archive")
    save_rule(page, drawer)
    open_page(page)
    page.locator(".ao-rule .ao-sentence").first.click()  # tap a sentence to edit it
    drawer = page.get_by_label("Edit rule")
    open_prompt(drawer)
    expect(drawer.get_by_label("prompt")).to_have_value("Only list tasks for Alice.")
    expect(drawer.get_by_label("model")).to_have_value("some/model")
    r = api.get("/ui/api/auto-organize").json()["rules"][0]
    assert r["action"]["prompt"] == "Only list tasks for Alice."
    assert_healthy(page)


def test_run_now_checks_pages(page, api):
    if not api.get("/ui/api/auto-organize/pipeline").json()["enabled"]:
        pytest.skip("page watch is off: run.sh without E2E_STUB_RMPAGES=0")
    open_page(page)
    drawer = start_pipeline_rule(page)
    save_rule(page, drawer)
    before = len(api.get("/ui/api/auto-organize/pipeline").json()["runs"])
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text("Pages checked:")).to_be_visible()
    # the header and the rule's status line show the run; the history lists it
    expect(page.locator(".ao-hdr-st")).to_contain_text("Pages: last check")
    expect(page.locator(".ao-rule .ao-meta")).to_contain_text("ran just now")
    page.get_by_role("tab", name="Page checks").click()
    expect(page.locator(".ao-prun").filter(has_text="run now").first).to_be_visible()
    assert len(api.get("/ui/api/auto-organize/pipeline").json()["runs"]) == before + 1
    assert "pipeline" not in page.inner_text("body").lower()
    fits_viewport(page)
    assert_healthy(page)


def test_rule_watching_a_deleted_folder_warns(page, api):
    if not api.get("/ui/api/auto-organize/pipeline").json()["enabled"]:
        pytest.skip("page watch is off: run.sh without E2E_STUB_RMPAGES=0")
    gone = "99999999-9999-4999-8999-999999999999"  # not in the tree
    cfg = api.get("/ui/api/auto-organize").json()
    cfg["rules"] = [{
        "id": "", "name": "", "enabled": True, "scope": "all",
        "source": {"id": "", "name": ""}, "destination": {"id": "", "name": ""},
        "trigger": {"type": "page_changed"},
        "action": {"type": "pipeline", "prompt": "", "model": ""},
        "watch": [{"id": gone, "kind": "folder", "name": "Old projects"}],
    }]
    r = api.put("/ui/api/auto-organize", data=cfg)
    assert r.ok, r.text()
    run = api.post("/ui/api/auto-organize/pipeline/run-now")
    assert run.ok, run.text()
    missing = api.get("/ui/api/auto-organize/pipeline").json()["missingWatches"]
    assert [m["watchId"] for m in missing] == [gone], missing
    open_page(page)
    rule = page.locator(".ao-rule")
    expect(rule.locator(".ao-sentence")).to_have_text("IF I write on a page in Old projects THEN extract tasks to my inbox")
    expect(rule.locator(".ao-ms.warn")).to_contain_text("Old projects: watched item no longer exists")
    fits_viewport(page)
    assert_healthy(page)


def test_move_rules_still_work(page, api):
    open_page(page)
    drawer = add_rule(page)
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_contain_text("named like “Paper*”")
    rules = api.get("/ui/api/auto-organize").json()["rules"]
    assert rules[0]["action"]["type"] == "move"
    assert_healthy(page)
