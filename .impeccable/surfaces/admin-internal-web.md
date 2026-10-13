---
version: 1
slug: "admin-internal-web"
primary_target: "admin/internal/web"
related_targets: []
---

# Surface brief: backsoon-admin panel

Scope: the whole admin panel (login, directory/home, page create/edit, delete confirm, destination-conflict confirm). Mode: Operate.
Audience and job: a self-hosting administrator, desktop or phone, often mid-redeploy. Primary task: check which page a destination shows. 1–10 pages per install. Must not read as a generic SaaS dashboard.
Constraints: Go html/template, no client framework, works without JavaScript; no external fonts or assets; English UI; WCAG 2.2 AA; 320 px; light and dark.

## Direction contract

THESIS: The panel is the lobby directory of the building your apps live in: every destination is a line on the board pointing to the page a visitor will see. It refuses the sidebar + stat cards + table admin.

OWN-WORLD: Felt-board ground (tinted near-black green in dark, warm ivory plate in light), warm-white letter ink, brass for rules, focus and the lookup answer, one vermilion signal reserved for change/conflict/warning. System UI face for chrome; monospace with tabular figures for destinations and dates, set like changeable letters on fixed pitch. One-pixel rule system. Domain and service:port carry two constant marks.

STORY: The administrator sees every destination and its page at a glance, believes it is exactly what backsoon serves, and edits or previews in one step.

FIRST VIEWPORT: Top bar: backsoon wordmark left, New page button and Sign out right. Below, the lookup line spanning the board ("Which page does … show?"). Then the directory: one block per page (display name, updated date, Preview and Edit), its destinations as letter lines with dot leaders; the default page last as "everything else". On phone the board is a single column; preview opens below its block.

FORM: Lobby directory board, my grounded list position 4, seed key efa48271. Signature move: the lookup answers in the board's grammar following backsoon's order (domain → service:port → default), lighting the winning line in brass while the rest dims.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Raises from declined challengers
- Ikeda (palette): felt, ink, brass and one signal only; no stray greys.
- Metro (type scale): a page's name gets poster scale in its edit view header.
- Streaming (focus): the focused block expands with its preview; others recede.
- Labanotation (encoding): constant marks for domain vs service:port.
- Centre rail (rules): one hairline system; every rule deliberate.

## Unresolved
- None blocking; exact token values decided at build.
