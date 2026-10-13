---
version: 1
slug: "admin-internal-web"
primary_target: "admin/internal/web"
related_targets: []
---

# Surface brief: backsoon-admin panel

Scope: the whole admin panel (login, pages list/home, page create/edit, delete confirm, destination-conflict confirm, warning confirm, errors). Mode: Operate.
Audience and job: a self-hosting administrator, desktop or phone, often mid-redeploy, who keeps Portainer and Nginx Proxy Manager open beside this panel. Primary task: check which page a destination shows. 1–10 pages per install.
Constraints: Go html/template, no client framework, works without JavaScript; no external fonts or assets; English UI; WCAG 2.2 AA; 320 px; light and dark.

## Direction contract

THESIS: backsoon reads as a sibling of Nginx Proxy Manager 2.x and Portainer: the same Tabler-family admin grammar a self-hoster already trusts, executed at full craft. It refuses period metaphors and invented chrome; familiarity is the feature.

OWN-WORLD: Light: gray-50 page ground (#f9fafb-like), white surfaces with a 1px cool-gray border and a soft offset shadow, gray-900 text, gray-500 secondary; dark: deep slate ground with raised slate surfaces. System UI font stack (as NPM), ui-monospace for destinations in light chips. backsoon's section color is a maintenance orange (Tabler-orange family, AA-checked for text/buttons), used for the card's top status line, the primary button and the lookup answer; state colors: green ok, orange/amber warning, red danger, blue focus ring. Line icons in one stroke weight (authored inline SVG).

STORY: The administrator recognises the layout instantly, sees every page with its destinations, checks which page a destination shows, and edits or previews in one step.

FIRST VIEWPORT: White header: backsoon mark + wordmark left, signed-in user and Sign out right. Main: one card "Maintenance pages" with an orange top status line; card header holds the title, the lookup field ("Which page does … show?") and the primary "Add page" button. Table: NAME (with "Updated …" under it), DESTINATIONS (mono chips with a domain or service:port icon), PREVIEW, and a row actions menu (details/summary, no JS). The default page is the last row, labelled "Everything else". A lookup answer appears as an alert above the table and highlights the winning row. On phones the rows become stacked blocks (no horizontal table scroll).

FORM: Canon (standing exit) chosen by the user after two rounds: "Portainer / Nginx Proxy Manager". Seed key efa48271 (round 1 assigned lobby directory rejected for colours and retro feel; round 2 re-rolled). Signature move: the lookup answers in backsoon's order (domain → service:port → default) and highlights the winning row in the table.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Craft bar
Reference captures of NPM 2.16 (login, proxy hosts light, 390 px) in .odd-checks/npm-ref/ (development only, not shipped). Match its craft level, spacing and table grammar; do better than it on mobile (no horizontal scroll) and honour prefers-color-scheme.

## Unresolved
- None blocking; exact token values decided at build.
