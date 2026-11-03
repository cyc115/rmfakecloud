"""Time triggers and tag actions: "IF a document was created in the last N … THEN add tag",
and the rule that takes the tag off again once the document is older. Built in the editor,
saved, run live, and checked through the planner (a second preview finds nothing left to
tag, the remove rule finds the tags). Runs on desktop and phone (390 px); the test ends with
the seeded PDFs untagged again, so the next parametrization starts clean.
"""
import os
import re

from playwright.sync_api import expect

from test_autoorganize_ui import assert_healthy, card, new_rule, open_page, pick_source, save_rule
from test_pipeline_ui import fits_viewport


def summary(drawer):
    return drawer.get_by_label("Summary").locator(".ao-sentence")


def preview(api, rule):
    r = api.post("/ui/api/automation/preview", data={"rule": rule})
    assert r.ok, r.text()
    # a draft preview is filtered to the draft rule
    return r.json()["moves"]


def test_tag_new_documents_then_untag(page, api):
    open_page(page)
    drawer = new_rule(page)
    card(drawer, "It was created…").click()
    expect(drawer.get_by_label("trigger compare")).to_have_value("within")
    expect(drawer.get_by_label("trigger amount")).to_have_value("24")
    expect(drawer.get_by_label("trigger unit")).to_have_value("hours")
    drawer.get_by_label("trigger amount").fill("2")
    drawer.get_by_label("trigger unit").select_option("days")
    pick_source(page, drawer, "Inbox")
    card(drawer, "Add a tag").click()
    expect(drawer.get_by_text("Enter the tag to add.")).to_be_visible()
    drawer.get_by_label("tag to change").fill(" new ")
    want = "IF a document was created in the last 2 days in Inbox THEN add tag “new”"
    expect(summary(drawer)).to_have_text(want)
    # the PDFs conftest uploads to Inbox were created this session
    expect(drawer.get_by_text("Matches now:")).to_contain_text(re.compile(r"[1-9]\d* documents?"))
    expect(drawer.locator(".ao-matches .ao-hint")).to_have_text("add tag “new”")
    # the THEN cards now include the two tag actions
    expect(drawer.get_by_role("radiogroup", name="Action").get_by_role("radio")).to_have_count(5)
    fits_viewport(page)
    if os.environ.get("E2E_SHOTS"):
        kind = "phone" if page.viewport_size["width"] < 500 else "desktop"
        os.makedirs(os.environ["E2E_SHOTS"], exist_ok=True)
        drawer.screenshot(path=f"{os.environ['E2E_SHOTS']}/editor-tag-{kind}.png")
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(want)
    page.reload()
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(want)
    (rule,) = api.get("/ui/api/automation").json()["rules"]
    assert rule["when"]["trigger"] == {"type": "created_time", "compare": "within", "amount": 2, "unit": "days"}, rule
    assert rule["actions"] == [{"type": "add_tag", "tag": "new"}], rule
    expect(page.locator(".ao-rule .ao-meta")).to_contain_text(re.compile(r"[1-9]\d* to tag now"))

    # live run: the tags are written
    page.locator(".ao-seg label", has_text="Live").click()
    expect(page.locator(".ao-seg label.on")).to_have_text("Live")
    page.get_by_role("button", name="Preview", exact=True).click()
    expect(page.locator(".ao-row .to", has_text="add tag “new”").first).to_be_visible()
    page.once("dialog", lambda d: d.accept())
    page.get_by_role("button", name=re.compile(r"Apply (this change|these \d+ changes)")).click()
    expect(page.get_by_text(re.compile(r"Documents checked: 0 moved, [1-9]\d* tagged, 0 errors"))).to_be_visible()
    expect(page.locator(".ao-rule .ao-meta")).to_contain_text("nothing to tag now")
    expect(page.locator(".ao-rule .ao-meta")).to_contain_text("tagged ")
    tagged = {m["docName"] for m in api.get("/ui/api/automation/log").json()["moves"] if m.get("action") == "add_tag"}
    assert "Paper" in tagged, tagged

    # the remove rule sees the tags the live run wrote. The real pair uses "more than 2 days
    # ago"; the seeded PDFs are minutes old, so this one says "in the last 2 days"
    remove = {"id": "", "name": "", "enabled": True,
              "when": {"subject": "document", "in": rule["when"]["in"],
                       "trigger": {"type": "created_time", "compare": "within", "amount": 2, "unit": "days"}},
              "conditions": [{"type": "tag_is", "tag": "new"}],
              "actions": [{"type": "remove_tag", "tag": "new"}]}
    planned = preview(api, remove)
    assert {m["docName"] for m in planned} >= {"Paper"}, planned
    assert all(m["action"] == "remove_tag" and m["from"] == m["to"] for m in planned), planned
    cfg = api.get("/ui/api/automation").json()
    cfg["rules"] = [remove]
    r = api.put("/ui/api/automation", data=cfg)
    assert r.ok, r.text()
    page.reload()
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(
        "IF a document was created in the last 2 days in Inbox AND tagged “new” THEN remove tag “new”")
    page.once("dialog", lambda d: d.accept())
    page.get_by_role("button", name="Run now").click()
    expect(page.get_by_text(re.compile(r"[1-9]\d* tagged, 0 errors"))).to_be_visible()
    assert preview(api, remove) == []
    expect(page.locator(".ao-rule .ao-meta")).to_contain_text("untagged ")
    assert_healthy(page)
