.PHONY: help proto build test lint docker-up docker-down docker-infra clean

SHELL := /bin/bash

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

proto-ts: ## Generate TypeScript protobuf types
	@echo "Generating TypeScript protobuf types..."
	@mkdir -p libs/proto-gen/src/ts
	@cd proto && npx buf generate --template buf.gen.yaml 2>/dev/null || \
		echo "Install buf CLI: https://buf.build/docs/installation"

proto-go: ## Generate Go protobuf types
	@echo "Generating Go protobuf types..."
	@mkdir -p libs/proto-gen/src/go
	@cd proto && buf generate --template buf.gen.yaml 2>/dev/null || \
		echo "Install buf CLI and protoc-gen-go"

proto-python: ## Generate Python protobuf types
	@echo "Generating Python protobuf types..."
	@mkdir -p libs/proto-gen/src/python
	@cd proto && buf generate --template buf.gen.yaml 2>/dev/null || \
		echo "Install buf CLI and betterproto"

proto-lint: ## Lint protobuf definitions
	@cd proto && buf lint

# ----------------------------------------------------------------------------
# Build
# ----------------------------------------------------------------------------

install: ## Install all dependencies
	npm install
	cd apps/ingest-service && go mod tidy
	cd apps/analytics-service && pip install -e ".[dev]"

build: ## Build all applications
	npx nx run-many --target=build --all --parallel=5

build-web: ## Build Angular frontend
	npx nx build web --configuration=production

build-api: ## Build NestJS API gateway
	npx nx build api-gateway

build-ingest: ## Build Go ingest service
	cd apps/ingest-service && CGO_ENABLED=1 go build -o ../../dist/apps/ingest-service/server ./cmd/server

build-analytics: ## Build Python analytics service (no-op, interpreted)
	@echo "Python analytics service requires no build step"

# ----------------------------------------------------------------------------
# Development
# ----------------------------------------------------------------------------

dev: ## Start all services in development mode
	@echo "Starting infrastructure..."
	docker-compose -f docker-compose.infra.yml up -d
	@echo "Waiting for services to be healthy..."
	@sleep 10
	@echo "Starting application services..."
	npx nx run-many --target=serve --projects=web,api-gateway --parallel=2

dev-web: ## Start Angular dev server
	npx nx serve web

dev-api: ## Start NestJS API gateway
	npx nx serve api-gateway

dev-ingest: ## Start Go ingest service
	cd apps/ingest-service && go run ./cmd/server

dev-analytics: ## Start Python analytics service
	cd apps/analytics-service && uvicorn sentinel_analytics.main:app --reload --port 5000

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

test-web: ## Run Angular tests
	npx nx test web

test-api: ## Run NestJS tests
	npx nx test api-gateway

test-ingest: ## Run Go tests
	cd apps/ingest-service && go test ./...

test-analytics: ## Run Python tests
	cd apps/analytics-service && pytest

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

db-migrate: ## Run database migrations
	npx nx run entity-service:migration:run

db-seed: ## Seed database with sample data
	npx ts-node scripts/seed-data.ts

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

# ----------------------------------------------------------------------------
# Cleanup
# ----------------------------------------------------------------------------

clean: ## Clean all build artifacts
	rm -rf dist/ node_modules/.cache
	cd apps/ingest-service && go clean
	find . -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true
