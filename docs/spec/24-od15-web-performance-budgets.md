# 24 — OD-15 web and API performance budgets (baseline)

**Status:** Baseline defined (Phase 0, 2026-10-03). Verification against fixtures is a separate gate.

## Representative fixture (unchanged from OD-15)

| Dimension | Value |
| --- | --- |
| Issues per workspace | 10,000 |
| Active issues per team | 1,000 |
| Concurrent signed-in sessions | 100 |

Budgets are **p95** unless noted. They describe acceptance targets for beta, not marketed capacity.

## Client (browser)

Measured on Chrome current stable, warm session (second navigation after bootstrap), desktop viewport ≥ 1280px.

| ID | Scenario | p95 target | Notes |
| --- | --- | --- | --- |
| WB-1 | Team board first paint after bootstrap completes | ≤ 2.5 s | From `DatabaseWrapper` ready to first kanban column visible |
| WB-2 | Open issue from board (side panel) | ≤ 800 ms | Click card → title editable or loading skeleton gone |
| WB-3 | Open issue full page (`/issue/KEY-n`) | ≤ 1.2 s | Cold route within warm workspace |
| WB-4 | SSE gap: user sees stale banner or refetch completes | ≤ 5 s | After forced sequence gap in soak test |
| WB-5 | Filter apply (client-side on synced store) | ≤ 400 ms | 1k visible issues on team |
| WB-6 | Keyboard shortcut to first issue (`O`) | ≤ 1.2 s | Same as WB-2 |

## API (same fixture, single API process, PostgreSQL on same host class as deploy README)

| ID | Endpoint / flow | p95 target | Notes |
| --- | --- | --- | --- |
| AB-1 | `GET` bootstrap / initial sync payload | ≤ 2.0 s | Full workspace sync for fixture size |
| AB-2 | `GET /api/v1/workspaces/{id}/swarm` | ≤ 500 ms | Floor page roster |
| AB-3 | `GET /api/v1/workspaces/{id}/metrics` | ≤ 800 ms | Metrics aggregates |
| AB-4 | `PATCH` issue field update | ≤ 300 ms | Single field, authorized member |
| AB-5 | `GET /api/v1/notifications` | ≤ 400 ms | Inbox hydrate |

## How to verify (later)

1. Seed or import fixture data matching the table above.
2. Record p95 with repeated runs (≥ 30) per scenario; discard first run (cold JIT).
3. Log results in the release checklist; fail beta if any WB-* or AB-* exceeds target by > 20% on two consecutive runs.

## Decision record

```text
Decision ID: OD-15 (partial)
Date: 2026-10-03
Owner/approvers: project owner
Chosen option: Numeric p95 budgets WB-1–WB-6 and AB-1–AB-5 on the 10k/1k/100 fixture
User/product rationale: Makes “fast enough” testable before beta without claiming unlimited scale
Security and privacy impact: none
Operational: requires representative seed + soak harness (not yet automated in CI)
Alternatives rejected: Ad hoc “feels fine” on Bravo only
Documents/contracts updated: this file; doc 10 OD-15 row; doc 11 work plan note
```
