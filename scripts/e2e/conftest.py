"""Shared fixtures for the browser e2e suite (run via scripts/e2e/run.sh).

Targets a live rmfakecloud seeded by run.sh (Inbox, Archive, Projects/Alpha,
notebook "E2E Notes", user e2e / e2epass123). E2E_BASE_URL overrides the URL.
"""
import os

import pytest
from playwright.sync_api import sync_playwright

USER = "e2e"
PASSWORD = "e2epass123"

PDF = (
    b"%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj "
    b"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj "
    b"3 0 obj<</Type/Page/MediaBox[0 0 200 200]/Parent 2 0 R>>endobj\n"
    b"trailer<</Root 1 0 R>>\n%%EOF\n"
)

MULTI_PAGES = 5


def multi_pdf(n=MULTI_PAGES):
    kids = " ".join(f"{3 + i} 0 R" for i in range(n))
    objs = [b"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj ",
            f"2 0 obj<</Type/Pages/Kids[{kids}]/Count {n}>>endobj ".encode()]
    objs += [f"{3 + i} 0 obj<</Type/Page/MediaBox[0 0 200 200]/Parent 2 0 R>>endobj ".encode() for i in range(n)]
    return b"%PDF-1.4\n" + b"".join(objs) + b"\ntrailer<</Root 1 0 R>>\n%%EOF\n"


LOGIN_JS = """async ([user, password]) => {
  const r = await fetch('/ui/api/login', {method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: JSON.stringify({email: user, password})});
  if (!r.ok) throw new Error('login failed: ' + r.status);
  const t = await r.text();
  localStorage.setItem('authToken', t);
  localStorage.setItem('currentUser', atob(t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  return t;
}"""


def find(entries, name):
    """Depth-first search of the /ui/api/documents tree by name."""
    for e in entries or []:
        if e.get("name") == name:
            return e
        hit = find(e.get("children"), name)
        if hit:
            return hit
    return None


@pytest.fixture(scope="session")
def base_url():
    return os.environ.get("E2E_BASE_URL", "http://10.0.0.11:3099").rstrip("/")


@pytest.fixture(scope="session")
def browser():
    with sync_playwright() as p:
        b = p.chromium.launch()
        yield b
        b.close()


def _context(browser, base_url, **kw):
    ctx = browser.new_context(base_url=base_url, **kw)
    pg = ctx.new_page()
    pg.goto("/login")
    token = pg.evaluate(LOGIN_JS, [USER, PASSWORD])
    ctx.set_extra_http_headers({"Authorization": f"Bearer {token}"})
    _reset_rules(ctx.request)
    pg.console_errors = []
    pg.on("console", lambda m: pg.console_errors.append(m.text) if m.type == "error" else None)
    pg.on("pageerror", lambda e: pg.console_errors.append(str(e)))
    return ctx, pg



@pytest.fixture
def api(browser, base_url):
    """Authenticated APIRequestContext (paths relative, e.g. /ui/api/documents)."""
    ctx, _ = _context(browser, base_url)
    yield ctx.request
    ctx.close()


@pytest.fixture(scope="session", autouse=True)
def seed_pdf(browser, base_url):
    """Paper.pdf in Inbox (uploads need the HTTP API; the rest is seeded by rmseed)."""
    ctx, _ = _context(browser, base_url)
    api = ctx.request
    docs = api.get("/ui/api/documents").json()
    inbox = find(docs["Entries"], "Inbox")
    assert inbox, "seed missing: Inbox folder"
    for name, buf in (("Paper", PDF), ("Multi", multi_pdf())):
        if not any(c["name"] == name for c in inbox.get("children") or []):
            r = api.post("/ui/api/documents/upload", multipart={
                "parent": inbox["id"],
                "file": {"name": name + ".pdf", "mimeType": "application/pdf", "buffer": buf}})
            assert r.ok, r.text()
    ctx.close()


def _reset_rules(api):
    cfg = api.get("/ui/api/automation").json()
    cfg["rules"] = []
    cfg["settings"]["mode"] = "dry"
    cfg["settings"]["enabled"] = True
    r = api.put("/ui/api/automation", data=cfg)
    assert r.ok, r.text()


@pytest.fixture(params=["desktop", "phone"])
def page(request, browser, base_url):
    """Logged-in page; parametrized over desktop and a 390x844 mobile viewport."""
    kw = {} if request.param == "desktop" else {
        "viewport": {"width": 390, "height": 844}, "is_mobile": True, "has_touch": True}
    ctx, pg = _context(browser, base_url, **kw)
    yield pg
    ctx.close()


@pytest.fixture
def phone_page(browser, base_url):
    """Logged-in 390x844 mobile page (not parametrized)."""
    ctx, pg = _context(browser, base_url, viewport={"width": 390, "height": 844},
                       is_mobile=True, has_touch=True)
    yield pg
    ctx.close()
