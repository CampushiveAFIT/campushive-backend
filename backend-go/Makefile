.PHONY: help build run test test-race vet fmt fmt-check tidy migrate-up migrate-down migrate-reset migrate-version migrate-latest migrate-force schemadiff clean

DATABASE_URL ?=
TEST_DATABASE_URL ?= postgresql://postgres:postgres@localhost:5432/campus_hive_test?sslmode=disable
TEST_MIGRATIONS_DATABASE_URL ?= postgresql://postgres:postgres@localhost:5432/campus_hive_migrations_test?sslmode=disable
SCHEMA_REFERENCE_DATABASE_URL ?=

help:
	@printf 'targets:\n'
	@printf '  build             compile every package\n'
	@printf '  run               start the API server\n'
	@printf '  test              run the full test suite\n'
	@printf '  test-race         run the test suite with the race detector\n'
	@printf '  vet               run go vet\n'
	@printf '  fmt / fmt-check   format the tree / verify formatting\n'
	@printf '  tidy              tidy go.mod\n'
	@printf '  migrate-up        apply all pending migrations\n'
	@printf '  migrate-down      revert every migration\n'
	@printf '  migrate-reset     revert and re-apply every migration\n'
	@printf '  migrate-version   print the applied schema version\n'
	@printf '  migrate-latest    print the newest embedded migration version\n'
	@printf '  migrate-force     clear recorded migration state after a failed run\n'
	@printf '  schemadiff        compare the live schema against SCHEMA_REFERENCE_DATABASE_URL\n'
	@printf '  clean             remove build output\n'

build:
	go build ./...

run:
	go run ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l . )" || (gofmt -l . && echo 'run make fmt' && exit 1)

tidy:
	go mod tidy

migrate-up:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate up

migrate-down:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate down

migrate-reset:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate reset

migrate-version:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate version

migrate-latest:
	go run ./cmd/migrate latest

migrate-force:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate force

schemadiff:
	@test -n "$(DATABASE_URL)" || (echo 'set DATABASE_URL' && exit 2)
	@test -n "$(SCHEMA_REFERENCE_DATABASE_URL)" || (echo 'set SCHEMA_REFERENCE_DATABASE_URL' && exit 2)
	go run ./cmd/schemadiff "$(DATABASE_URL)" "$(SCHEMA_REFERENCE_DATABASE_URL)"

clean:
	go clean ./...
