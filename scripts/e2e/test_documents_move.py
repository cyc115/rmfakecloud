"""Drag and drop moves in the document browser (file list rows -> folders, list and sidebar)."""
import pytest
from playwright.sync_api import expect

from conftest import PDF, _context, find
from test_autoorganize_ui import assert_healthy


@pytest.fixture
def desktop(browser, base_url):
    ctx, pg = _context(browser, base_url)
    yield pg, ctx.request
    ctx.close()


def entries(api):
    return api.get("/ui/api/documents").json()["Entries"]


def parent_of(api, name):
    def walk(es, parent):
        for e in es or []:
            if e.get("name") == name:
                return parent
            hit = walk(e.get("children"), e["id"])
            if hit is not None:
                return hit
        return None
    return walk(entries(api), "")


def seed(api):
    inbox = find(entries(api), "Inbox")
    if not find(inbox.get("children"), "Move target"):
        assert api.post("/ui/api/folders", data={"name": "Move target", "parentId": inbox["id"]}).ok
    if not find(entries(api), "Movable"):
        r = api.post("/ui/api/documents/upload", multipart={
            "parent": inbox["id"], "file": {"name": "Movable.pdf", "mimeType": "application/pdf", "buffer": PDF}})
        assert r.ok, r.text()
    return find(entries(api), "Inbox")["id"], find(entries(api), "Move target")["id"]


def row(page, name):
    return page.locator(".filelist-item", has_text=name).first


def test_drag_a_file_onto_a_folder_row_moves_it(desktop, base_url):
    page, api = desktop
    inbox, target = seed(api)
    page.goto(f"{base_url}/documents/{inbox}")
    row(page, "Movable").drag_to(row(page, "Move target"))
    expect(page.get_by_text("Moved 1 item to Move target")).to_be_visible()
    assert parent_of(api, "Movable") == target
    expect(row(page, "Movable")).to_have_count(0)  # gone from Inbox's list
    assert_healthy(page)


def test_drag_onto_the_sidebar_root_moves_to_my_files(desktop, base_url):
    page, api = desktop
    _, target = seed(api)
    page.goto(f"{base_url}/documents/{target}")
    if parent_of(api, "Movable") != target:  # make the test order-independent
        assert api.put("/ui/api/documents", data={"documentId": find(entries(api), "Movable")["id"],
                                                  "parentId": target, "name": ""}).ok
        page.reload()
    row(page, "Movable").drag_to(page.locator(".documents-tree").get_by_text("My Files", exact=True))
    expect(page.get_by_text("Moved 1 item to My Files")).to_be_visible()
    assert parent_of(api, "Movable") == ""
    assert find(entries(api), "Movable")["name"] == "Movable"  # the name is kept


def test_a_folder_cannot_go_inside_itself(desktop):
    _, api = desktop
    inbox, target = seed(api)
    r = api.put("/ui/api/documents", data={"documentId": inbox, "parentId": target, "name": ""})
    assert r.status == 400 and parent_of(api, "Move target") == inbox
