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
