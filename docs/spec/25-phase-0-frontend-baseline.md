# 25 — Phase 0 frontend baseline (UX + WCAG spot audit)

**Date:** 2026-10-03  
**Scope:** Static review of `web/` + `@converge/ui`, aligned with Bravo behavior through A1–A9. Not a formal Chair re-score.

## Surfaces reviewed

Sign-in, workspace sidebar, team board (kanban + filters), side issue panel, full issue page, Floor (`/floor`), Metrics, Inbox, Views empty state, delete issue dialog, keyboard shortcuts dialog.

## One-sentence verdict

The UI is coherent for desktop power users and agent/evidence workflows; accessibility and mobile are the main gaps before a public beta score above 8.

## Directional dimension scores (0–10)

| Dimension | Score | Note |
| --- | --- | --- |
| Resolution | 7 | Ticket id, empty states, honest metrics labels |
| Integrity | 6 | Dual store/REST paths; URL now `/floor` with `/swarm` redirect |
| Action | 7 | Hotkeys C/O/S/M/U/F/? documented in-app |
| Economy | 6 | Dense board; section spacing improved on issue detail |
| Recovery | 6 | Named toasts on save failure; conflict UI still thin |
| Instrument | 6 | Metrics/Floor readable; OD-15 budgets now numbered |
| Trust | 7 | Evidence sections, delete confirms issue key |

**Overall (informal):** ~6.5 — gate Integrity/Trust formal pass still needs WCAG work and R-9 plan.

## WCAG 2.2 AA spot audit (sample)

| Check | Result | Finding |
| --- | --- | --- |
| 1.4.3 Contrast (text) | Pass (sample) | Muted foreground on board metadata readable in light theme |
| 2.1.1 Keyboard | Partial | Global shortcuts strong; some actions remain click-only in settings |
| 2.4.1 Bypass blocks | Fail | No skip link to `#board` or main content |
| 2.4.7 Focus visible | Partial | Board `#board` programmatic focus; combobox focus ring inconsistent |
| 4.1.2 Name, role, value | Partial | Metadata comboboxes use `role="combobox"`; side panel close needs visible name audit |
| 1.4.10 Reflow | Fail | `body { overflow: hidden }`, fixed sidebar — not usable &lt; 768px |

**Priority fixes for Phase 1:** skip link, focus ring tokens, 409 conflict dialog, SSE stale indicator.

## Phase 0 deliverables checklist

| Item | Status |
| --- | --- |
| `/floor` route + permanent redirect from `/swarm` | Done |
| Document title uses Floor for `/floor` | Done |
| In-app keyboard map (sections + header entry) | Done |
| OD-15 p95 budgets documented | Done (`24-od15-web-performance-budgets.md`) |
| Formal Chair re-score | **Not done** — scheduled after Phase 1 integrity items |
