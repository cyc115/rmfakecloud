"""Tags in the document viewer: shown, added and removed by hand (document and page)."""
import pytest
from playwright.sync_api import expect

from conftest import _context, find
from test_autoorganize_ui import assert_healthy

NB = "55555555-5555-4555-8555-555555555555"  # the seeded notebook "E2E Notes" (page p1)


@pytest.fixture
def desktop(browser, base_url):
    ctx, pg = _context(browser, base_url)
    yield pg, ctx.request
    ctx.close()


def tags(api, doc):
    r = api.get(f"/ui/api/documents/{doc}/tags")
    assert r.ok, r.text()
    return r.json()


def test_add_and_remove_a_document_tag(desktop, base_url):
    page, api = desktop
    page.goto(f"{base_url}/documents/{NB}")
    field = page.get_by_label("Add a tag to document")
    field.fill("E2E Doc Tag")
    field.press("Enter")
    expect(page.get_by_label("Remove tag E2E Doc Tag")).to_be_visible()
    assert "E2E Doc Tag" in tags(api, NB)["tags"]
    page.get_by_label("Remove tag E2E Doc Tag").click()
    expect(page.get_by_label("Remove tag E2E Doc Tag")).to_have_count(0)
    assert "E2E Doc Tag" not in tags(api, NB)["tags"]
    assert_healthy(page)


def test_add_and_remove_a_page_tag(desktop, base_url):
    page, api = desktop
    page.goto(f"{base_url}/documents/{NB}?page=1")
    field = page.get_by_label("Add a tag to page 1")
    field.fill("e2e-page")
    field.press("Enter")
    expect(page.get_by_label("Remove tag e2e-page")).to_be_visible()
    assert "e2e-page" in tags(api, NB)["pages"][0]["tags"]
    page.get_by_label("Remove tag e2e-page").click()
    expect(page.get_by_label("Remove tag e2e-page")).to_have_count(0)
    assert "e2e-page" not in tags(api, NB)["pages"][0]["tags"]
