"""Baseline Automation UI tests (document rules); every test runs on desktop and phone."""
import re

from playwright.sync_api import expect

from conftest import find

GOOD_URL = "/automation"


def open_page(page):
    page.goto(GOOD_URL)
    expect(page.get_by_role("heading", name="Automation", exact=True)).to_be_visible()


def assert_healthy(page):
    assert "Something went wrong" not in page.inner_text("body")
    assert page.console_errors == []


def pick(page, listbox, name):
    # option text carries a leading ▸/▾ glyph, so match the folder name itself
    page.get_by_role("listbox", name=listbox).get_by_role("option").filter(
        has_text=re.compile(rf"^\W*{re.escape(name)}$")).click()


def new_rule(page):
    page.get_by_role("button", name="+ New").click()
    drawer = page.get_by_label("Edit rule")
    expect(drawer).to_be_visible()
    return drawer


def card(drawer, title):
    return drawer.get_by_role("radio", name=title, exact=True)


def pick_source(page, drawer, name):
    drawer.get_by_role("button", name="change", exact=True).click()
    pick(page, "source folder", name)


def add_rule(page, source="Inbox", dest="Archive", pattern="Paper*"):
    """A document rule: Its name matches <pattern> in <source> → Move it to <dest>."""
    drawer = new_rule(page)
    card(drawer, "Its name matches").click()
    expect(card(drawer, "Move it to…")).to_have_attribute("aria-checked", "true")
    pick_source(page, drawer, source)
    drawer.get_by_label("pattern").fill(pattern)
    pick(page, "destination folder", dest)
    return drawer


def save_rule(page, drawer):
    drawer.get_by_role("button", name="Save", exact=True).click()
    expect(page.get_by_text("Rule saved")).to_be_visible()


def inbox_docs(api):
    docs = api.get("/ui/api/documents").json()
    inbox = find(docs["Entries"], "Inbox")
    return [c["name"] for c in inbox.get("children") or []]


def test_page_loads_with_dry_mode_and_no_rules(page):
    open_page(page)
    expect(page.get_by_text("No automations yet")).to_be_visible()
    expect(page.locator(".ao-seg label.dry.on")).to_have_text("Dry-run")
    expect(page.locator(".ao-hdr .ao-status")).to_contain_text("Dry-run · checks every")
    expect(page.get_by_role("button", name="Run now")).to_be_visible()
    assert_healthy(page)


def test_create_move_rule_preview_lists_matching_docs(page):
    open_page(page)
    drawer = add_rule(page)
    expect(drawer.get_by_text("Matches now:")).to_contain_text("1 document")
    drawer.get_by_role("button", name="▸ show").click()
    expect(page.get_by_text("would move")).to_be_visible()
    expect(page.locator(".ao-row .doc", has_text="Paper")).to_be_visible()
    assert_healthy(page)


def test_save_rule_persists_after_reload(page):
    open_page(page)
    save_rule(page, add_rule(page))
    expect(page.locator(".ao-rule")).to_have_count(1)
    page.reload()
    rule = page.locator(".ao-rule")
    expect(rule).to_have_count(1)
    expect(rule.locator(".ao-sentence")).to_have_text("IF a document named like “Paper*” is in Inbox THEN move it to Archive")
    assert_healthy(page)


def test_run_now_in_dry_mode_logs_planned_move(page):
    open_page(page)
    save_rule(page, add_rule(page))
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text("Dry run: 1 would move")).to_be_visible()
    page.get_by_role("tab", name="Runs").click()
    expect(page.locator(".ao-runs .ao-chip.dry").first).to_have_text("DRY")
    expect(page.locator(".ao-runs")).to_contain_text("1 would")
    api = page.context.request
    assert "Paper" in inbox_docs(api)  # nothing moved
    assert_healthy(page)


def test_delete_rule(page):
    open_page(page)
    save_rule(page, add_rule(page))
    page.once("dialog", lambda d: d.accept())
    page.get_by_label("Rule actions").click()
    page.get_by_role("button", name="Delete", exact=True).click()
    expect(page.get_by_text("No automations yet")).to_be_visible()
    assert_healthy(page)


def test_validation_error_is_shown(page):
    open_page(page)
    drawer = add_rule(page, source="Inbox", dest="Inbox")
    expect(drawer.get_by_text("Destination is the same as the source")).to_be_visible()
    expect(drawer.get_by_role("button", name="Save", exact=True)).to_be_disabled()
    assert_healthy(page)
