"""?page=N deep link of the PDF viewer (desktop and phone)."""
import re

from playwright.sync_api import expect

from conftest import MULTI_PAGES, find
from test_autoorganize_ui import assert_healthy


def expect_page(page, n):
    expect(page.get_by_label("Page number")).to_have_value(str(n))
    expect(page.get_by_text(f"of {MULTI_PAGES}")).to_be_visible()


def doc_id(api, name):
    return find(api.get("/ui/api/documents").json()["Entries"], name)["id"]


def test_page_param_opens_that_page_and_paging_updates_the_url(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}?page=2")
    expect_page(page, 2)
    page.get_by_role("button", name="Next page").click()
    expect_page(page, 3)
    expect(page).to_have_url(re.compile(rf"/documents/{mid}\?page=3$"))


def test_page_param_is_clamped(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}?page=99")
    expect_page(page, MULTI_PAGES)
    expect(page).to_have_url(re.compile(rf"\?page={MULTI_PAGES}$"))
    assert_healthy(page)  # no console error (react-pdf "Invalid page")


def test_no_param_starts_at_page_one(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}")
    expect_page(page, 1)


def test_typing_a_page_number_goes_there(page, api, base_url):
    mid = doc_id(api, "Multi")
    page.goto(f"{base_url}/documents/{mid}")
    field = page.get_by_label("Page number")
    expect_page(page, 1)
    field.fill("4")
    field.press("Enter")
    expect_page(page, 4)
    expect(page).to_have_url(re.compile(rf"\?page=4$"))
    field.fill("99")          # beyond the end -> last page
    field.press("Enter")
    expect_page(page, MULTI_PAGES)
    field.fill("")            # cleared -> back to the current page
    field.press("Enter")
    expect_page(page, MULTI_PAGES)
    assert_healthy(page)
