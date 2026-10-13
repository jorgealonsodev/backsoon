# backsoon developer entry points. Every target needs Docker; none touches production.
# Go 1.26+ is also needed for lint (gofmt, go vet) and test (admin Go tests).
.PHONY: lint test e2e e2e-npm e2e-admin e2e-up e2e-down

# Static checks: compose files, nginx -t on the rendered config, NPM snippet, and for the panel
# gofmt + go vet (admin/).
lint:
	sh test/lint.sh

# Service tests (curl against the built nginx image), then the panel's Go tests.
test:
	sh test/unit.sh
	cd admin && go test -count=1 ./...

# Local NPM harness (compose.test.yml). e2e-up starts the base stack; the panel profile is
# started with BACKSOON_TEST_WITH_ADMIN=1.
e2e-up:
	sh test/harness.sh up

e2e-down:
	sh test/harness.sh down

# Both suites run one after the other, each in its own fresh disposable project that it removes
# on exit (default projects backsoon-e2e and backsoon-e2e-admin; if BACKSOON_TEST_PROJECT is
# set both reuse it, which is safe because they never overlap). Do not use `make -j`: they share
# the loopback ports. A failing first suite stops the run.
e2e:
	sh test/e2e.sh
	sh test/admin-e2e.sh

# NPM fallback suite only (backsoon service + global snippet).
e2e-npm:
	sh test/e2e.sh

# Panel suite only (real NPM + backsoon + backsoon-admin).
e2e-admin:
	sh test/admin-e2e.sh
