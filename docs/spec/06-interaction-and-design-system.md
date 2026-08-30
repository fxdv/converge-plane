# 06 — Interaction and design system

The design goal is original, calm, information-dense software. Tegon demonstrates useful patterns—compact grouped issues, direct property editing, side-panel detail, command/search shortcuts—but its assets, exact geometry, and visual expression are not source material.

## Interaction principles

1. Direct actions are visible before they become shortcuts.
2. One interaction has one save model: explicit submit for authored content, immediate transactional selection for discrete properties.
3. Optimistic feedback never claims success before the server accepts the mutation.
4. Keyboard focus and browser history survive panels, dialogs, filters, and responsive transitions.
5. Drag-and-drop is an accelerator with equivalent menu and keyboard operations.
6. Dense presentation never reduces target size, contrast, semantics, or error clarity below accessibility requirements.

## Keyboard system

Shortcuts are disabled while typing in editable fields unless explicitly listed as form actions. `Cmd` maps to macOS and `Ctrl` to Windows/Linux. A visible shortcut-help panel documents current context.

| Shortcut | Scope | Action | Release |
| --- | --- | --- | --- |
| `C` | Global, non-input | Create issue | MVP |
| `/` | Global, non-input | Open/focus search | MVP |
| `Cmd/Ctrl+K` | Global | Open command palette | MVP |
| `F` | Issue collection, non-input | Open filters | MVP |
| `J` / `K` | Focused issue collection | Next/previous issue | MVP |
| `Enter` | Focused row/card/result | Open issue | MVP |
| `Esc` | Dialog/panel/palette/filter | Cancel or close one layer and restore focus | MVP |
| `Cmd/Ctrl+Enter` | Issue/comment form | Submit | MVP |
| `Cmd/Ctrl+B` | Desktop shell | Toggle sidebar | MVP |
| `?` | Global, non-input | Show shortcut help | MVP |
| Arrow keys + `Space` | Focused Kanban/item menu | Select/move using accessible control | MVP |

Single-letter destructive shortcuts are prohibited. Shortcut handlers use a centralized registry with scope, priority, input suppression, discoverable label, and collision tests.

## Command palette

The palette groups commands into Navigation, Create, Current issue, and Settings. It:

- returns only permitted destinations/actions;
- ranks exact object keys and command names predictably;
- shows shortcut hints and team/workspace context;
- supports keyboard and pointer operation with correct combobox/listbox semantics;
- never becomes the only route to a feature;
- does not execute destructive actions without the ordinary confirmation contract.

MVP commands: create issue, go to My issues, choose team issues/triage, search, open account/workspace/team settings, toggle theme/sidebar. NEXT/LATER commands appear only with those releases.

## Filter model

MVP fields are status, assignee, priority, label, and team where workspace scope applies.

| Field type | Operators | Value semantics |
| --- | --- | --- |
| Enum/reference | is, is not | Multiple selected values are OR within the clause |
| Set label | includes any, excludes all | Exact label IDs, rendered by current authorized names |
| Assignment | is me, is unassigned, is user, is not | User picker restricted by team access |

Different field clauses are AND by default. Any future nested boolean builder must display grouping explicitly and serialize to a versioned structured query. Filters are reflected in URL state, removable individually, keyboard reachable, and validated on load. Natural-language-to-filter conversion is excluded.

## Drag-and-drop

- MVP Kanban drag changes status only.
- Drop targets announce destination and invalid reasons before commit.
- Pointer drop sends one versioned mutation; card shows pending then saved/reverted.
- Keyboard/menu “Move to status” provides equivalent behavior.
- Auto-scroll is bounded; reduced-motion preference disables animated reordering.
- Drag cannot cross teams or perform NEXT planning/relationship changes implicitly.
- Concurrent changes return a conflict and refetch the affected card/columns.

## Inline editing

| Content | Save model | Cancel/recovery |
| --- | --- | --- |
| Status, priority, assignee, labels | Commit on explicit selection | Escape closes without change; server failure reverts field |
| Title | Enter/explicit save; optional blur only after user-visible pending state | Escape restores prior value; conflict preserves attempted text |
| Description/comment | Explicit submit or `Cmd/Ctrl+Enter` | Draft persists for current account/workspace; Escape never discards silently |
| NEXT dates/project/cycle | Commit on explicit selection/confirmation | Invalid dependency remains editable with explanation |

Every editable control exposes label, current value, pending state, success/error announcement, and permission-disabled explanation. There is no silent background autosave of long-form content.

## Notification interaction

NEXT notifications use three layers:

1. transient toast for the current user’s direct action result;
2. in-app Inbox for asynchronous events requiring awareness;
3. badge/count as a summary, never the only signal.

Toasts are not permanent records and do not contain sensitive content when the page context may change. Inbox groups bursts, supports read/unread and mute, and re-authorizes target content. Email/Slack delivery is not part of NEXT.

## Layout and spacing tokens

Base spacing is 4px. Components use tokens rather than one-off values.

| Token | Value | Typical use |
| --- | --- | --- |
| `space-0` | 0 | Reset |
| `space-1` | 4px | Icon/text micro-gap |
| `space-2` | 8px | Compact control gap |
| `space-3` | 12px | Row/card padding |
| `space-4` | 16px | Standard section padding |
| `space-6` | 24px | Panel/section separation |
| `space-8` | 32px | Page section separation |
| `space-12` | 48px | Empty/onboarding vertical rhythm |

Minimum interactive target is 44×44px on touch layouts. Dense desktop rows may be visually 36px high only when the interactive hit area and focus outline remain at least 40px and adjacent targets do not overlap.

Layout tokens:

- content reading width: 720px;
- settings/form width: 640px;
- desktop sidebar: 232px default, collapsible;
- issue detail panel: 360–480px depending on viewport, with 400px as the MVP default;
- radius: 6px controls, 8px cards/panels, 12px dialogs;
- border: 1px semantic border; shadows reserved for overlays/elevation.

## Typography

Use a system sans stack to reduce payload and improve platform rendering: `ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif`. Issue keys/code use `ui-monospace, "SFMono-Regular", Consolas, monospace`.

| Token | Size / line height | Weight | Use |
| --- | --- | --- | --- |
| `text-xs` | 12 / 16px | 400–600 | Metadata, never essential unlabeled action text |
| `text-sm` | 14 / 20px | 400–600 | Default dense UI and rows |
| `text-md` | 16 / 24px | 400–600 | Forms, comments, descriptions |
| `text-lg` | 20 / 28px | 600 | Panel/page section title |
| `text-xl` | 24 / 32px | 650–700 | Primary page/onboarding heading |
| `text-2xl` | 30 / 40px | 700 | Rare marketing/empty hero, not application chrome |

Body text does not fall below 14px; 12px metadata must meet contrast and remain zoomable. At 200% zoom, no primary task requires two-dimensional scrolling except Kanban/data surfaces where horizontal scrolling is intrinsic and labeled.

## Color system

Colors are semantic CSS custom properties. User-defined workflow/label colors are rendered with generated foreground/border combinations that meet contrast; color is never the sole status indicator.

| Role | Light | Dark |
| --- | --- | --- |
| Canvas | `#F8FAFC` | `#0B1220` |
| Surface | `#FFFFFF` | `#111827` |
| Elevated/hover | `#F1F5F9` | `#1E293B` |
| Primary text | `#0F172A` | `#F8FAFC` |
| Secondary text | `#475569` | `#CBD5E1` |
| Border | `#CBD5E1` | `#475569` |
| Accent | `#2563EB` | `#60A5FA` |
| Accent hover | `#1D4ED8` | `#93C5FD` |
| Focus ring | `#7C3AED` | `#C4B5FD` |
| Success | `#15803D` | `#4ADE80` |
| Warning | `#A16207` | `#FACC15` |
| Danger | `#B91C1C` | `#F87171` |
| Informational | `#0369A1` | `#38BDF8` |

Final combinations require automated and manual WCAG contrast validation in both themes. Muted/disabled text remains readable and disabled state also uses affordance/semantics, not low opacity alone.

## Component inventory

MVP primitives: Button, IconButton, Link, TextField, Textarea/rich editor shell, Select/Combobox, Checkbox, Radio, Switch, Menu, Tooltip, Dialog, Sheet, Popover, Toast, Banner, Tabs, Breadcrumb, Avatar, Badge, Status, Label, Skeleton, EmptyState, ErrorState, IssueRow, IssueCard, GroupHeader, FilterChip, CommandPalette, SearchResults, ActivityItem, Comment.

NEXT adds NotificationItem, AttachmentUploader/Item, RelationPicker, ViewBuilder, ProjectCard/Progress, CycleCard/Progress. LATER adds IntegrationCard/ScopeReview/DeliveryLog, TokenSecret, WebhookDelivery.

Each component documents anatomy, variants, keyboard behavior, focus order, ARIA expectations, loading/error/disabled state, long text, localization expansion, and theme snapshots.

## Responsive behavior

| Range | Behavior |
| --- | --- |
| `<768px` | Navigation drawer; list-first; full-screen detail/create/settings; property controls stack; Kanban offers column selector and move menu |
| `768–1023px` | Overlay/collapsible sidebar; detail full page by default; dialogs may become sheets |
| `≥1024px` | Persistent/collapsible sidebar; list/Kanban; optional side detail |
| `≥1440px` | Wider collections; detail panel may coexist without compressing readable content |

No component relies on hover. Safe-area insets, virtual keyboard, orientation, reduced motion, and 200% zoom are test cases. Native mobile apps and full offline editing are excluded.

## Accessibility acceptance

- WCAG 2.2 AA for MVP and every later release.
- Complete primary journeys with keyboard only and a representative screen reader.
- Visible focus never obscured; focus returns after overlays; no keyboard trap.
- Status/priority/label meaning has text or accessible name in addition to color/icon.
- Live announcements are concise and do not repeat on every realtime list update.
- Drag operations have accessible alternatives and instructions.
- Touch targets, contrast, reflow, reduced motion, error identification, and form labels pass automated plus manual review.
- Axe/static checks run in CI, but manual acceptance remains required for issue creation, list/Kanban, detail, filters/search, and settings.
