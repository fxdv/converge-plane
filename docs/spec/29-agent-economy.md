# 29 — Phase 10, agent economy desk

**Updated:** 2026-10-07

The run ledger and the 7-day governance line stay. This phase makes the 24-hour cap visible and counts the refusals that used to be only a 422.

| Item | Status |
| --- | --- |
| Team caps on `GET /workspaces/{id}/swarm` `economy.teams` (spent, cap, remaining) | Done |
| Spend by agent in the same 24-hour window (reported cost, tokens, runs, issues) | Done |
| Budget and Done-evidence refusals counted from `audit_events` | Done |
| Cap 422 names the team and the micro-USD remaining | Done |
| No server price table; cost stays agent-reported | Done |

The Floor page (`web/src/modules/swarm/swarm-page.tsx`) renders the desk above the 7-day line. Contract text is in `docs/spec/08-api-and-event-contracts.md`. Direction row P10 is in `docs/spec/11-direction-v2.md`.
