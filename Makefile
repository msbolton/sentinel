.PHONY: help proto proto-check proto-test build test lint docker-up docker-down docker-infra clean db-schema db-seed chart-lint

SHELL := /bin/bash

# Go services
GO_SERVICES := api-gateway ingest-service entity-service track-service search-service link-analysis-service alert-service analytics-service

# ============================================================================
# SENTINEL - Geospatial Intelligence Platform
# ============================================================================

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ----------------------------------------------------------------------------
# Protobuf Generation
# ----------------------------------------------------------------------------

proto: proto-ts proto-go proto-python ## Generate all protobuf code

proto-ts: ## Generate TypeScript protobuf types (libs/proto-gen/src/ts)
	@echo "Generating TypeScript protobuf types..."
	@cd proto && PATH="../node_modules/.bin:$$PATH" buf generate --template buf.gen.ts.yaml
	@echo "Stripping proto enum prefixes from generated TS enum values..."
	@perl -pi -e 's/^(  )([A-Z][A-Z_0-9]*) = "[A-Z_0-9]+",$$/$$1$$2 = "$$2",/' libs/proto-gen/src/ts/sentinel/v1/*.ts

proto-go: ## Generate Go protobuf types (libs/proto-gen/src/go, module github.com/sentinel/proto-gen)
	@echo "Generating Go protobuf types..."
	@cd proto && buf generate --template buf.gen.go.yaml

proto-python: ## Generate Python protobuf types (libs/proto-gen/src/python, package sentinel-proto)
	@echo "Generating Python protobuf types..."
	@cd proto && buf generate --template buf.gen.python.yaml

proto-lint: ## Lint protobuf definitions
	@cd proto && buf lint

proto-check: proto-lint ## Lint and check wire compatibility against main
	@cd proto && buf breaking --against '../.git#branch=main,subdir=proto'

proto-fixtures: ## Rewrite the cross-language golden fixtures in libs/proto-gen/testdata
	@cd libs/proto-gen/src/go && go test ./sentinel/v1/ -run TestGoldenFixtures -update

proto-test: ## Run the Go, TypeScript and Python contract tests
	@cd libs/proto-gen/src/go && go test -race ./...
	@npx nx test proto-gen
	@cd libs/proto-gen/src/python && PYTHONPATH=. python3 -m pytest -q tests

# ----------------------------------------------------------------------------
# Build
# ----------------------------------------------------------------------------

install: ## Install all dependencies
	npm install
	@for svc in $(GO_SERVICES); do \
		echo "Tidying $$svc..."; \
		cd apps/$$svc && go mod tidy && cd ../..; \
	done
	cd libs/common-go && go mod tidy

build: ## Build all applications
	npx nx run-many --target=build --all --parallel=5

build-client: ## Build Angular frontend
	npx nx build client --configuration=production

build-api: ## Build Go API gateway
	npx nx build api-gateway

build-go: ## Build all Go services (including the API gateway)
	@for svc in $(GO_SERVICES); do \
		echo "Building $$svc..."; \
		npx nx build $$svc; \
	done

build-entity: ## Build Go entity service
	npx nx build entity-service

build-track: ## Build Go track service
	npx nx build track-service

build-search: ## Build Go search service
	npx nx build search-service

build-links: ## Build Go link analysis service
	npx nx build link-analysis-service

build-alerts: ## Build Go alert service
	npx nx build alert-service

build-ingest: ## Build Go ingest service
	npx nx build ingest-service

build-analytics: ## Build Go analytics service
	npx nx build analytics-service

# ----------------------------------------------------------------------------
# Development
# ----------------------------------------------------------------------------

dev: ## Start all services in development mode
	@echo "Starting infrastructure..."
	docker-compose -f docker-compose.infra.yml up -d
	@echo "Waiting for services to be healthy..."
	@sleep 10
	@echo "Starting application services..."
	npx nx run-many --target=serve --projects=client,api-gateway,entity-service,track-service,search-service,link-analysis-service,alert-service,ingest-service --parallel=10

dev-client: ## Start Angular dev server
	npx nx serve client

dev-api: ## Start Go API gateway (AUTH_DEV_BYPASS=true)
	npx nx serve api-gateway

dev-entity: ## Start Go entity service
	npx nx serve entity-service

dev-track: ## Start Go track service
	npx nx serve track-service

dev-search: ## Start Go search service
	npx nx serve search-service

dev-links: ## Start Go link analysis service
	npx nx serve link-analysis-service

dev-alerts: ## Start Go alert service
	npx nx serve alert-service

dev-ingest: ## Start Go ingest service
	npx nx serve ingest-service

dev-analytics: ## Start Go analytics service
	npx nx serve analytics-service

# ----------------------------------------------------------------------------
# Docker
# ----------------------------------------------------------------------------

docker-infra: ## Start infrastructure services only
	docker-compose -f docker-compose.infra.yml up -d

docker-up: ## Start all services
	docker-compose up -d

docker-down: ## Stop all services
	docker-compose down

docker-clean: ## Stop all services and remove volumes
	docker-compose down -v

docker-logs: ## Tail all service logs
	docker-compose logs -f

docker-build: ## Build all Docker images
	docker-compose build

# ----------------------------------------------------------------------------
# Testing
# ----------------------------------------------------------------------------

test: ## Run all tests
	npx nx run-many --target=test --all --parallel=5

test-client: ## Run Angular tests
	npx nx test client

test-api: ## Run Go API gateway tests
	npx nx test api-gateway

test-go: ## Run all Go tests
	@for svc in $(GO_SERVICES); do \
		echo "Testing $$svc..."; \
		npx nx test $$svc; \
	done

test-entity: ## Run entity service tests
	npx nx test entity-service

test-track: ## Run track service tests
	npx nx test track-service

test-search: ## Run search service tests
	npx nx test search-service

test-links: ## Run link analysis service tests
	npx nx test link-analysis-service

test-alerts: ## Run alert service tests
	npx nx test alert-service

test-ingest: ## Run ingest service tests
	npx nx test ingest-service

test-analytics: ## Run analytics service tests
	npx nx test analytics-service

# ----------------------------------------------------------------------------
# Linting
# ----------------------------------------------------------------------------

lint: ## Lint all code
	npx nx run-many --target=lint --all --parallel=5

lint-fix: ## Fix linting issues
	npx nx run-many --target=lint --all --parallel=5 -- --fix

# ----------------------------------------------------------------------------
# Database
# ----------------------------------------------------------------------------

db-schema: ## Apply db/schema/*.sql in name order to the running postgres container (idempotent; re-run after edits)
	@for f in db/schema/*.sql; do echo "== $$f"; docker exec -i sentinel-postgres psql -U sentinel -d sentinel -v ON_ERROR_STOP=1 -q < "$$f" || exit 1; done

db-seed: ## Seed database with sample entities, a geofence rule, and locations
	docker exec -i sentinel-postgres psql -U sentinel -d sentinel -v ON_ERROR_STOP=1 < db/seed/seed.sql

# ----------------------------------------------------------------------------
# Kubernetes
# ----------------------------------------------------------------------------

k8s-deploy-dev: ## Deploy to development K8s cluster
	kubectl apply -k k8s/overlays/dev

k8s-deploy-staging: ## Deploy to staging K8s cluster
	kubectl apply -k k8s/overlays/staging

k8s-deploy-prod: ## Deploy to production K8s cluster
	@echo "WARNING: Deploying to production!"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 1
	kubectl apply -k k8s/overlays/prod

chart-lint: ## Lint the Helm chart and check it renders db/schema into the schema Job (needs helm; run helm dependency build k8s/charts/sentinel first)
	helm lint k8s/charts/sentinel --set ingestService.serviceToken=lint
	helm template sentinel k8s/charts/sentinel --set ingestService.serviceToken=lint | grep -q '^  70_feeds\.sql: '

# ----------------------------------------------------------------------------
# Cleanup
# ----------------------------------------------------------------------------

clean: ## Clean all build artifacts
	rm -rf dist/ node_modules/.cache
	@for svc in $(GO_SERVICES); do \
		cd apps/$$svc && go clean && cd ../..; \
	done
	cd libs/common-go && go clean
	find . -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true
