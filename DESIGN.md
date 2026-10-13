# Design

The backsoon admin panel (`admin/internal/web`) is a sibling of Nginx Proxy Manager 2.x and Portainer: the Tabler-family admin grammar self-hosters already use, with backsoon's own section colour. Familiarity is the point; craft is in the details.

The visitor-facing maintenance page (`templates/maintenance.html`) is a separate artifact with its own minimal styles. The panel never restyles it.

## Foundations

| Token | Light | Dark | Use |
|-------|-------|------|-----|
| `--bg` | `#f9fafb` | `#0f1724` | Page ground |
| `--surface` | `#ffffff` | `#172233` | Header, cards, inputs |
| `--surface-2` | `#f3f4f6` | `#1c2940` | Table head, card footer |
| `--border` | `#e5e7eb` | `#2b3a4f` | Card and row rules (1px) |
| `--border-input` | `#838c9b` | `#6b7c95` | Input borders (≥ 3:1) |
| `--text` | `#111827` | `#e8ecf2` | Body text |
| `--muted` | `#5b6472` | `#9aa8bc` | Secondary text (≥ 4.5:1 on surface and chips) |
| `--btn` | `#c2410c` | `#c2410c` | Primary button (white text 5.18:1) |
| `--accent-text` | `#9a3412` | `#fdba74` | Lookup answer, matched row |
| `--line` | `#f76707` | `#f97316` | Card top status line |
| `--focus` | `#2563eb` | `#60a5fa` | Focus ring |
| `--ok` / `--warn` / `--bad` / `--info` | green / amber / red / blue | lighter tints | Alerts and pills only |

- **Colour strategy:** restrained. Neutrals carry the page; maintenance orange is backsoon's section colour, used only for the card status line, the primary action, the lookup answer and the matched row. State colours appear only where they carry meaning.
- **Scheme:** follows `prefers-color-scheme`; no toggle (no JavaScript).
- **Type:** the system UI stack (as NPM) for everything; the `ui-monospace` stack only for destinations and the lookup field. Fixed rem scale, no fluid type, no web fonts.
- **Shape and depth:** 6px radius; 1px borders; one soft offset shadow for cards and one for popovers. No glass, gradients or hard shadows.
- **Icons:** authored inline SVG, one line weight. No emoji or Unicode glyphs as icons.

## Components

- **Header:** mark and wordmark left; signed-in user and Sign out (POST with CSRF) right.
- **Card:** white surface with a 2–3px orange top status line, header (title + tools), body, and a grey footer for actions or hints. Never nested.
- **Pages table:** small uppercase column heads (NAME, DESTINATIONS, PREVIEW). Each row shows the page name with "Updated …" beneath, destination chips, a Preview button and a `details`/`summary` actions menu (Edit, Preview full size, Delete). The default page is always last, with an "Everything else" chip and no delete.
- **Destination chip:** light mono chip with a globe icon (domain) or a server icon (service:port); the type is also visually-hidden text.
- **Lookup:** search field with icon, Check button. The answer is an orange-tinted alert above the card naming the page and the rule that matched; the winning row is tinted and its chip outlined, with a "Match" pill.
- **Alerts and pills:** tinted background with matching text; amber "No destinations" pill for pages that own none.
- **Forms:** labels above inputs, help text below, blue focus ring; "Start from" as two radio cards; primary and secondary buttons in the card footer; a separate red-outlined "Danger zone" card for delete.
- **Confirm pages:** one card per decision (content warnings, destination move, delete) with the facts and two actions; no modals.
- **Preview:** sandboxed `iframe` (no tokens) inside the expanded row or the edit page; links inside it show a framed notice instead of breaking.

## Layout and motion

- Content width 72rem (44rem for forms and confirm pages, 25rem for login); gutter 1rem (1.5rem from 40rem).
- Below 48rem the table becomes stacked row blocks (no horizontal scroll); chips wrap whole. Below 40rem the card tools stack and the lookup field takes the full row.
- Motion only for state (border and background colour, 150 ms ease-out); `prefers-reduced-motion` removes it.

## Accessibility

WCAG 2.2 AA in both schemes; one `h1` per page; every input labelled, errors tied with `aria-describedby`; visible focus on every control; usable at 320 px; works without JavaScript.

## Provenance

Chosen by the owner after two direction rounds ("Portainer / Nginx Proxy Manager"); reference captures of NPM 2.16 were used during development only. No raster assets ship with the panel.
