# 链上分析平台 V1 - Makefile
.PHONY: infra-up infra-down infra-ps infra-logs migrate migrate-ch backend-dev worker-dev frontend-dev test bench fmt build push

infra-up:
	cd deploy && docker compose --env-file .env up -d

infra-down:
	cd deploy && docker compose --env-file .env down

infra-ps:
	cd deploy && docker compose ps

infra-logs:
	cd deploy && docker compose logs -f

migrate:
	cd backend && go run ./cmd/migrate

migrate-ch:
	cd backend && go run ./cmd/migrate -target clickhouse

backend-dev:
	cd backend && go run ./cmd/api

worker-dev:
	cd backend && go run ./cmd/worker

frontend-dev:
	cd frontend && pnpm dev

test:
	cd backend && go test ./...

# Sprint 1/2 遗留验收引入的契约守卫，CI 应逐步接入同一条命令。
check: test
	python3 scripts/check_openapi.py
	cd frontend && node scripts/check-token-contrast.mjs
	@echo "所有静态契约检查通过"

bench:
	cd backend && go test -bench=. -benchmem ./internal/...

fmt:
	cd backend && gofmt -w .

build:
	cd backend && go build -o bin/api ./cmd/api && go build -o bin/worker ./cmd/worker

push:
	python3 scripts/sync_to_vm.py
