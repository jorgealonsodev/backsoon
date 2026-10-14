# backsoon: Strix a047 hardening
Objective: patch the findings of the Strix white-box report `backsoon-strix_a047` (copy in `.odd-checks/strix-a047/`, gitignored: `penetration_test_report.md`, `vuln-0001.md`).
Branch: fix/strix-a047-hardening from main (9e4cfb7). Delivery (push, merge) stays the user's decision.

## Specs

- S1 — Maintenance pages served to visitors cannot run scripts. The user chose "Bloquear scripts (Recomendada)": "CSP estricta en /__backsoon: ningún script ni recurso externo se ejecuta para los visitantes (encaja con RF-11 'sin scripts'). Se sigue avisando al subir, y el aviso pasa a decir que los scripts NO se ejecutarán." The report's header set, verbatim: `Content-Security-Policy default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; frame-ancestors 'none'; base-uri 'none'` and `X-Content-Type-Options nosniff`, as `add_header ... always` inside `location = /__backsoon` in `nginx/default.conf.template`.
- S2 — Report short-term item 2, verbatim: "Mirror the CSP/nosniff pair on the `/__backsoon.json` and `/__backsoon.empty` sibling locations so every response the static service emits carries the same header posture." Existing headers (Cache-Control, Retry-After, X-Backsoon, each exactly once) are unchanged.
- S3 — The upload checker (`Inspect` in `admin/internal/pages/content.go`) warns about the script-capable class the report lists, verbatim: "inline `on*=` handlers (`onerror`, `onload`, `onfocus`, `ontoggle`, `onbegin`, `onplay`, `onstart`, `onchange`, `onmouseenter`, `marquee onstart`), malformed tag contacts (`<script\n>`, `<script/x>`, unterminated `<script src=x`), `<iframe srcdoc="<svg onload=alert(1)>">`, `<base href="javascript:">`, `<form action="javascript:...">`, `<meta http-equiv=refresh>` to `javascript:` (including entity-encoded `javascript&#58;`), `<a href="javascript:...">`, `<table background="javascript:...">`, `<isindex action="javascript:...">`, `<noscript>`-wrapped meta refresh, `<embed type="image/svg+xml">`, and CSS `url(javascript:...)`". Still a warning with confirmation, never a rejection (RF-17). The shipped `templates/maintenance.html` and the panel's template-created pages stay warning-free.
- S4 — The script warning no longer says scripts will run: per S1, "el aviso pasa a decir que los scripts NO se ejecutarán". English and Spanish panel texts change together; README/PRD wording that promises scripts run is updated.

## Tasks

- T1 — S1, S2: nginx headers + `test/service-lib.sh` expect_maintenance assertions (all 503 responses, also through NPM in e2e). Route: inline. Commit: 214313d. RED 48 failed (only the two new headers), GREEN unit 353/0, lint pass.
- T2 — S3, S4: checker hardening, warning text (pages + web catalog EN/ES), Go tests, PRODUCT.md. Route: inline. Commit: c9501fb. RED 25 shapes + 2 panel text tests failed, look-alikes passed; GREEN go test ./... 8 ok, lint pass.
- T3 — Verify S1-S4 independently (gentle-ai-verify). Verdict S1-S4 pass, no severe finding; unit 353/0, lint pass, go 8/8. Its low false positive (template values with "JavaScript:") fixed in 5b2172c (RED 2 tests, GREEN go 8/8, lint pass).

Not done, with reason (report items): 3 (CSRF before multipart parse) — the token lives in the multipart body, the body is already bounded by MaxBytesReader and the route needs a session; 4 (x/crypto openpgp) — not imported, no runtime exposure, nothing to pin within one module; 5 (hard reject) — the user chose warn + block; 6 (read_only static container), 7 (Accept map narrowing) — informational follow-ups, not findings.

## Log

- L1 — User: "/mnt/480GB/Proyectos/Personales/20261013_backsoon/20261013_backsoon_git/backsoon-strix/strix_runs/backsoon-strix_a047/penetration_test_report.md pachea los problemas"
- L2 — User choice on script policy: "Bloquear scripts (Recomendada)".
- L3 — Verify (mv8lqofo-2-2zy0): S1-S4 pass. Low: javascript: matched in plain text, including template values -> fixed in 5b2172c. Accepted as advisory (CSP is the control): ' on<x>=' inside attribute values and commented markup warn; java&#9;script:, &#106;avascript:, <img alt=">" onerror> are not warned. Info: framed apps now show a blocked frame (frame-ancestors 'none'); external meta refresh is not warned. Not run: e2e through NPM, real browser render.
- L4 — Production check (Oracle Ampere, after the Portainer redeploy of 9d806de): from inside nginx-proxy-manager-app, `curl -D - -H 'Host: clipbin.net' http://backsoon/` returned 503 with Cache-Control, Retry-After 10, X-Backsoon 1, the exact S1 CSP and nosniff, each once; backsoon-npm-snippet logged "unchanged; no reload needed"; `nginx -T` in NPM includes @backsoon. Not checked: a public domain with its app stopped, through Cloudflare.
