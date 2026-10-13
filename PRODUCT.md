# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Self-hosting administrators who run their applications as Docker stacks in Portainer behind Nginx Proxy Manager (NPM). They install backsoon from the open-source repository and are the single administrator of their own instance. They use the panel equally on desktop and on a phone, for example to check or change a maintenance page while a redeploy is in progress.

## Product Purpose
backsoon shows each application's own maintenance page while that application is down (typically during a redeploy from Portainer) instead of NPM's generic 502, and hands traffic back automatically when the application responds again. The admin panel (backsoon-admin) is where the administrator uploads or creates those pages, assigns them to destinations, and sees every page that exists. Success: a page can be created, assigned and verified in a minute, without touching NPM or restarting anything.

## Positioning
One global NPM snippet plus a static nginx: adding an application never touches NPM configuration. Pages are chosen by domain first, then by the proxy host's service:port, then a default page. The panel is optional at runtime: if it stops, backsoon keeps serving pages.

## Operating Context
- Docker + Portainer + Nginx Proxy Manager; one Portainer stack; pages live in named volumes.
- The panel is published through an NPM proxy host with HTTPS and no published ports; it can additionally sit behind an NPM Access List.
- Typical session: a redeploy is about to happen or is happening; the administrator opens the panel, finds the application's page, checks its destinations and preview, or uploads a replacement.

## Capabilities and Constraints
- Single administrator defined by environment variables (user, bcrypt hash or plaintext alternative, session secret); no registration, roles or password recovery.
- Pages: list (default page always present and undeletable), upload a self-contained .html (max 512 KB, UTF-8) or create from the template (application name, message, language, icon), assign destinations (domain or service:port; one owner per destination, moving requires confirmation), replace HTML, delete (destinations fall back to the default page), sandboxed preview.
- Warnings, not rejections, for external resources or scripts in uploaded HTML.
- Atomic publication; changes are visible on the next request.
- Tech (decided): Go standard library + bcrypt, html/template, embed, distroless/static image; no database; no client-side framework required.
- Terminology: page, destination (domain or service:port), default page, preview, publish.
- Undecided: none recorded beyond the PRD.

## Brand Commitments
- Name: backsoon (lowercase).
- Panel UI language: English.
- Standing visual preference (user, round 2): the panel must sit alongside Portainer and Nginx Proxy Manager 2.x — familiar Tabler-family admin grammar, contemporary, not retro. Rejected: the lobby-directory world (colours, retro feel).
- Maintenance page template (templates/maintenance.html) is a separate visitor-facing artifact; its default page content is Spanish and self-contained. The panel must not restyle that template implicitly.

## Evidence on Hand
- PRD.md (requirements, acceptance criteria, runbook).
- templates/maintenance.html and the seeded default page.
- No users, testimonials, metrics or screenshots exist; do not fabricate any.

## Product Principles
1. The administrator is in a hurry during a redeploy: the right page and its destinations must be findable at a glance.
2. Never let the panel break serving: publication is atomic and the panel is not a runtime dependency.
3. Uploaded HTML is untrusted: preview it safely and warn clearly, without blocking legitimate pages.
4. Honest state: what the panel shows is exactly what backsoon serves.

## Accessibility & Inclusion
WCAG 2.2 AA for the panel; fully usable by keyboard and at 320 px width; respects prefers-color-scheme and prefers-reduced-motion.
