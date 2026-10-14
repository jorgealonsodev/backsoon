# Backsoon admin panel in English and Spanish
Objective: the backsoon-admin panel speaks English and Spanish, chosen automatically from the browser with a visible ES/EN selector.
Branch: feat/admin-i18n from main 5c11720. Delivery (push, merge) stays the user's decision.

## Specs

- S1 — "quiero meterle español al panel tambien": every user-visible text of the panel exists in English and Spanish: page titles, headings, labels, hints, buttons, notices, confirmations, error pages, the 401/frame notices, validation errors and content warnings (including those produced by `admin/internal/pages`), screen-reader-only text and `aria-*` labels. `<html lang>` matches the language shown. Spanish is neutral and professional (no voseo).
- S2 — The user chose "Automático según el navegador + selector" ("Usa el idioma del navegador (Accept-Language) y un enlace ES/EN en la barra para cambiarlo, recordado en una cookie"). Resolution order for every response: a valid `backsoon_lang` cookie (`en` or `es`), then `Accept-Language` (q-values honoured; region subtags match their primary language, e.g. `es-ES` → `es`; unsupported languages skipped), then English.
- S3 — The selector is a visible ES/EN control in the navbar when signed in and on the login page. It works without JavaScript (the panel CSP keeps `script-src 'none'`). Choosing a language sets `backsoon_lang` (Path=/, about one year, HttpOnly, Secure, SameSite=Strict) and returns to the page the user was on.
- S4 — The selector route is reachable without a session (the login page needs it) and cannot be abused: an unknown language changes nothing; the return target is only a local path starting with a single `/` (not `//`, not `/\`, no scheme or host), otherwise `/`. No other route becomes reachable without a session.
- S5 — Unchanged behaviour: with no cookie and no `Accept-Language` the panel is English exactly as today, so existing tests stay green; log lines (RNF-9) stay English; the maintenance page template and each page's own Language field are untouched; sessions, CSRF, rate limiting and security headers are unchanged. Responses whose language can depend on `Accept-Language` or the cookie send `Vary: Accept-Language, Cookie` (they are already `Cache-Control: no-store`).
- S6 — Tests through the public interface: language resolution (cookie, Accept-Language with q-values and regions, fallback), the selector (cookie attributes, redirect, rejected languages, open-redirect attempts), Spanish rendering of every view (login, directory, form, confirm, error, notices) and Spanish validation errors and warnings; `test/admin-e2e.sh` checks through NPM that `Accept-Language: es` gives the Spanish login and that the selector switches language.
- S7 — Docs: README "Admin panel" says the panel is in English and Spanish and how the language is chosen; DESIGN.md is updated if it states copy or language rules.
- S8 — "documentacion tambien en español": the user-facing documentation also exists in Spanish: `README.es.md` (translation of README.md) and `docs/install.es.md` (translation of docs/install.md), neutral professional Spanish, same structure, commands, code blocks, variable names and anchors' meaning; each English and Spanish document links to its counterpart at the top. Developer documents (docs/npm-harness.md, DESIGN.md, PRODUCT.md, PRD.md, odd/) stay English.
- S9 — "Y la instalacion tiene que ser muy sencilla de entender": the install guide (both languages) and the README's Install section open with one short, linear quick path for the common case (Portainer Git stack with the automatic NPM install): what you need, the variables to set (a copy-paste block, each line explained in plain words, how to generate the hash and the secret, how to find the NPM network, its range and NPM's data directory), deploy, create the panel's proxy host (`backsoon-admin:8080`, SSL, Force SSL; saving it activates the fallback), and how to check that it works. Everything else (manual snippet install, plain `docker compose`, verification details, rollback, updating NPM/backsoon, what has been verified, PRD differences) moves after the quick path as reference, without losing content.

## Tasks

- T1 | S1–S7 | delegated worker (admin/internal/web/**, admin/internal/pages/** for message codes only, test/admin-e2e.sh, README.md, DESIGN.md) | pending | commit: —
- T2 | S1–S6 | independent verify (security: new session-less route) | pending | commit: —
- T3 | S8–S9 | delegated worker after T1 (README.md, README.es.md, docs/install.md, docs/install.es.md) | pending | commit: —

## Log

L1 — User (2026-10-14): "quiero meterle español al panel tambien"
L2 — User chose "Automático según el navegador + selector" over a BACKSOON_ADMIN_LANG variable or Spanish only.
L3 — Exploration: ~90 user-visible strings in admin/internal/web/*.go, 6 templates (layout, login, directory, form, confirm, error) plus framenotice.go; pages-package errors reach the UI through manage.go:163-178 (prefix-stripped err.Error()), directory.go:43 and content warnings in admin/internal/pages/content.go; responses already Cache-Control: no-store (views.go:221).
L4 — User (2026-10-14, while T1 was being delegated): "documentacion tambien en español". Parent scope: Spanish counterparts of the user-facing README and install guide (S8, T3), sequenced after T1 because both touch README.md.
L5 — User (2026-10-14): "Y la instalacion tiene que ser muy sencilla de entender". Parent scope: S9 (quick-path-first install guide and README Install section, both languages), folded into T3.
