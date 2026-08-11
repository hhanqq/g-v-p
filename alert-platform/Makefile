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
## golangci-lint, ESLint и PostgreSQL integration/migration тесты
## (нужен Docker — testcontainers-go поднимает реальную временную БД).
quality: check go-quality web-quality pg-integration-test migration-test

go-quality:
	cd $(GOPLATFORM) && $(GOLANGCI_LINT) run ./...

web-quality:
	npm --prefix $(WEB) run lint

## pg-integration-test — раздел XI ТЗ: internal/pipeline (ingest→dedup→
## resolve, FOR UPDATE SKIP LOCKED) и internal/adminapi (resolveGrant/RBAC)
## против реальной временной PostgreSQL (testcontainers-go, требует Docker).
pg-integration-test:
	cd $(GOPLATFORM) && go test -tags=integration ./internal/pipeline/... ./internal/adminapi/...

## migration-test — все database/migrations/*.sql на чистой БД, затем
## повторно на той же БД (идемпотентность — CLAUDE.md: «делай повторное
## применение безопасным»).
migration-test:
	cd $(GOPLATFORM) && go test -tags=integration ./internal/testutil/...
