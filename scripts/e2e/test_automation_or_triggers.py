"""“Or” triggers: a document rule fires when any one of its triggers matches
(when.orTriggers). Built in the editor (desktop and phone), previewed, saved, reloaded;
the API refuses or-triggers on page rules."""
import re

from playwright.sync_api import expect

from test_autoorganize_ui import assert_healthy, card, new_rule, open_page, pick_source, save_rule
from test_pipeline_ui import fits_viewport


def summary(drawer):
    return drawer.get_by_label("Summary").locator(".ao-sentence")


def test_finished_reading_or_name(page, api):
    open_page(page)
    drawer = new_rule(page)
    card(drawer, "I finish reading").click()
    pick_source(page, drawer, "Inbox")
    drawer.get_by_role("listbox", name="destination folder").get_by_role("option").filter(
        has_text=re.compile(r"^\W*Archive$")).click()
    # the seeded PDF was never read: nothing matches on finished reading alone
    matches = drawer.get_by_text("Matches now:")
    expect(matches).to_contain_text("0 documents")
    drawer.get_by_role("button", name="+ or another trigger").click()
    expect(drawer.get_by_label("or 1 trigger")).to_have_value("tag_is")
    drawer.get_by_label("or 1 trigger").select_option("name_matches")
    drawer.get_by_label("or 1 pattern").fill("Paper*")
    expect(summary(drawer)).to_contain_text("I finish reading a document OR a document named like “Paper*” is in Inbox")
    # any one trigger: the PDF now matches by its name
    expect(matches).to_contain_text(re.compile(r"1 document\b"))
    fits_viewport(page)
    sentence = summary(drawer).inner_text()
    save_rule(page, drawer)
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(sentence)
    page.reload()
    expect(page.locator(".ao-rule .ao-sentence")).to_have_text(sentence)
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert r["when"]["trigger"]["type"] == "finished_reading", r
    assert r["when"]["orTriggers"] == [{"type": "name_matches", "pattern": "Paper*"}], r
    assert [c["type"] for c in r["conditions"]] == ["kind_is"] and r["conditions"][0]["kind"] == "pdf_epub", r
    # reopen: the or row is there; remove it and the rule is single-trigger again
    page.locator(".ao-rule .ao-sentence").click()
    drawer = page.get_by_label("Edit rule")
    expect(drawer.get_by_label("or 1 pattern")).to_have_value("Paper*")
    drawer.get_by_role("button", name="remove or 1").click()
    expect(summary(drawer)).not_to_contain_text(" OR ")
    save_rule(page, drawer)
    (r,) = api.get("/ui/api/automation").json()["rules"]
    assert "orTriggers" not in r["when"], r
    assert_healthy(page)


def test_page_rule_refuses_or_triggers(api):
    rule = {"enabled": True,
            "when": {"subject": "page", "in": [{"id": "", "kind": "folder"}], "trigger": {"type": "page_changed"},
                     "orTriggers": [{"type": "page_tagged", "tag": "x"}]},
            "conditions": [], "actions": [{"type": "save_notes"}]}
    r = api.post("/ui/api/automation/rules", data=rule)
    assert r.status == 400, (r.status, r.text())
    assert "only document rules" in r.json()["error"], r.json()
