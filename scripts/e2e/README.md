# Browser e2e

`scripts/e2e/run.sh [pytest args]` builds the image on the services VM (`mike@10.0.0.11`),
seeds a fresh data dir with `cmd/rmseed`, starts container `rmfc-e2e` on :3099, runs
Playwright (Chromium) from this machine, then removes the container, data dir and temp tree.

- Seed: folders `Inbox`, `Archive`, `Projects/Alpha`; notebook `E2E Notes` (page `p1` = `strokes_a.rm`);
  `Paper` (PDF) in `Inbox`, uploaded by `conftest.py`. Login `e2e` / `e2epass123`.
- Fixtures (`conftest.py`): `base_url`, `page` (desktop and phone, logged in, rules reset),
  `phone_page` (390x844), `api` (authenticated `APIRequestContext`).
- `E2E_KEEP=1` leaves the container up; point pytest at it with
  `E2E_BASE_URL=http://10.0.0.11:3099 uv run --with pytest --with playwright python -m pytest scripts/e2e`.
- `RM_PAGES_URL` and `PAGEWATCH_MIN_AGE` pass through to the container when set.
- If Chromium is missing: `uv run --with playwright playwright install chromium`.
- Every logged-in context (`page`, `phone_page`, `api`) starts with Auto-organize rules reset (dry mode, no rules).
- `E2E_STUB_RMPAGES=1` runs a stub rm-pages (page watch on, nothing processed) for the pipeline UI tests.
- **Full pipeline (`test_pipeline_e2e.py`)** needs the real rm-pages (LLM calls, ~$0.001/run) and the
  homelab vault writer; it skips otherwise:
  `RM_PAGES_URL=http://host.containers.internal:8801 PAGEWATCH_MIN_AGE=1ms E2E_WRITER=~/workspace/github/homelab/scripts/rm-pages-vault/writer.py scripts/e2e/run.sh -k pipeline_e2e`.
  `PAGEWATCH_MIN_AGE=0s` means the 10 m default, so use `1ms`. rm-pages reads user e2e's blobs via
  `RMPAGES_EXTRA_ROOTS=e2e=/home/mike/rmfc-e2e-data` (homelab `ansible/rm-pages.yml`). The vault is a
  temp dir; the live timeline is read (plus one `POST /api/sync` round) to prove e2e events never show.
  run.sh deletes every rm-pages row of user e2e (events, jobs, results, trail, llm_requests, contact)
  before and after a real-rm-pages run.
