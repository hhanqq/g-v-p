GOPLATFORM := go-platform
WEB := web
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null || echo "$$(go env GOPATH)/bin/golangci-lint")

.PHONY: check quality \
	go-check go-quality \
	python-check \
	web-check web-quality \
	compose-check \
	pg-integration-test migration-test

## check — быстрая проверка перед коммитом (соответствует CLAUDE.md
## «Быстрая проверка»): формат, сборка, тесты без внешних БД.
check: go-check python-check web-check compose-check

go-check:
	cd $(GOPLATFORM) && gofmt -l cmd internal | tee /tmp/gofmt-check.out; \
		test ! -s /tmp/gofmt-check.out || (echo "gofmt: требуется 'gofmt -w cmd internal'" >&2; exit 1)
	cd $(GOPLATFORM) && go vet ./...
	cd $(GOPLATFORM) && go test ./...

python-check:
	python3 -m compileall -q packages services scripts tests
	python3 -m pytest -q

web-check:
	npm --prefix $(WEB) run build

compose-check:
	docker compose config -q

## quality — полная проверка (соответствует CLAUDE.md «Проверки перед
## завершением», раздел X-XI ТЗ Quality Gate): всё из check плюс
## golangci-lint и ESLint.
quality: check go-quality web-quality

go-quality:
	cd $(GOPLATFORM) && $(GOLANGCI_LINT) run ./...

web-quality:
	npm --prefix $(WEB) run lint

## pg-integration-test / migration-test — раздел XI ТЗ (PostgreSQL
## integration тесты на реальной временной БД, миграции на чистой и
## существующей БД). Ещё не реализовано — не входит в quality, чтобы
## make quality не давал ложно-зелёный результат по незакрытому пункту.
pg-integration-test:
	@echo "pg-integration-test: не реализовано (раздел XI ТЗ Quality Gate)" >&2; exit 1

migration-test:
	@echo "migration-test: не реализовано (раздел XI ТЗ Quality Gate)" >&2; exit 1
