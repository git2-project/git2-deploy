# git2-deploy: local development.
#
# Target names match the other git2 repos (run, tests, lint, proto,
# proto-check). Every recipe is a single command with no shell syntax, so it
# also runs on a Windows host, where GNU make falls back to cmd.exe.

GO     ?= go
GOTOOL := $(GO) tool -modfile=tools.mod

.PHONY: run tests lint proto proto-check

## run: build and start the Compose stack (needs compose/.env, see compose/env.example)
run:
	docker compose --project-directory compose up -d --build

## tests: Go tests, then the Compose smoke test (needs Docker)
tests:
	$(GO) test -count=1 ./...
	bash tests/smoke/compose_smoke.sh

## lint: buf lint and buf format for the proto files, go vet for Go
lint:
	$(GOTOOL) buf lint
	$(GOTOOL) buf format -d --exit-code
	$(GO) vet ./...

## proto: lint the proto files and regenerate gen/go
proto:
	$(GOTOOL) buf lint
	$(GOTOOL) buf generate

## proto-check: fail if gen/go is out of date with proto/ (compares with the git index)
proto-check:
	$(GOTOOL) buf generate
	git diff --exit-code -- gen/
