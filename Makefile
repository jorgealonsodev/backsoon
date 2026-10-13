# backsoon developer entry points. Every target needs Docker; none touches production.
.PHONY: lint test e2e e2e-up e2e-down

# Static checks: compose files, nginx -t on the rendered config, NPM snippet (once it exists).
lint:
	sh test/lint.sh

# Service tests (curl against the built nginx image). Admin Go tests join in U4: there are no
# admin Go packages yet, so there is nothing for `go test` to run.
test:
	sh test/unit.sh

# Local NPM harness (compose.test.yml); e2e.sh is added with the NPM integration (T3).
e2e-up:
	sh test/harness.sh up

e2e-down:
	sh test/harness.sh down

e2e:
	sh test/e2e.sh
