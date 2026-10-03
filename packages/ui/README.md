# @converge/ui

Shared UI primitives and icons for the Converge web app (Tegon-derived, AGPL).

## Typography

- **Sans:** Geist Sans (`font-sans` on the app shell).
- **Mono:** Geist Mono for issue keys and token names (`font-mono`).
- **Scale:** Tailwind defaults — `text-xs` metadata, `text-sm` body and controls, `text-base` app default, `text-md`/`text-lg` sparingly for headings inside dialogs.
- **Weight:** Prefer `font-medium` for labels; avoid `font-bold` on dense boards.

## Spacing

- **Issue detail sections:** `mt-6` (24px) between major blocks; horizontal padding `px-6` on detail columns. Use `IssueDetailSection` in the web app.
- **Board density:** Keep card row padding tight; section gaps are where readability is won.
- **Sidebar:** 190px fixed width (app layout).

## Color

- Use semantic tokens: `text-foreground`, `text-muted-foreground`, `bg-background`, `border-border`, `text-primary` / `bg-primary` from the theme — not hardcoded hex in app CSS.

## Icons

- **Default:** import from `@converge/ui/icons`.
- **Avoid** adding new `lucide-react` or `@remixicon/react` icons unless no Converge icon exists; migrate lucide call sites when touching a file.

## Dates

- **New code:** `date-fns` via `web/src/common/lib/format-date.ts`.
- **Legacy:** `dayjs` and `javascript-time-ago` remain in untouched paths.
