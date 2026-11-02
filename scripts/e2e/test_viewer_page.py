"""?page=N deep link of the PDF viewer (desktop and phone)."""
import re

from playwright.sync_api import expect

from conftest import MULTI_PAGES, find
from test_autoorganize_ui import assert_healthy


def doc_id(api, name):
    return find(api.get("/ui/api/documents").json()["Entries"], name)["id"]


def test_page_param_opens_that_page_and_paging_updates_the_url(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}?page=2")
    expect(page.get_by_text(f"Page: 2 of {MULTI_PAGES}")).to_be_visible()
    page.get_by_role("button", name="Next page").click()
    expect(page.get_by_text(f"Page: 3 of {MULTI_PAGES}")).to_be_visible()
    expect(page).to_have_url(re.compile(rf"/documents/{mid}\?page=3$"))


def test_page_param_is_clamped(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}?page=99")
    expect(page.get_by_text(f"Page: {MULTI_PAGES} of {MULTI_PAGES}")).to_be_visible()
    expect(page).to_have_url(re.compile(rf"\?page={MULTI_PAGES}$"))
    assert_healthy(page)  # no console error (react-pdf "Invalid page")


def test_no_param_starts_at_page_one(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}")
    expect(page.get_by_text(f"Page: 1 of {MULTI_PAGES}")).to_be_visible()
