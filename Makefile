.PHONY: images-push images-local help proto proto-check proto-test build test lint docker-up docker-down docker-infra clean db-schema db-seed chart-lint

SHELL := /bin/bash

# Go services
GO_SERVICES := api-gateway ingest-service entity-service track-service search-service link-analysis-service alert-service analytics-service ew-sensor

# Container images (the names the Helm chart pulls as sentinel/<name>). The
# ew-sensor simulator is not deployed, so it is not listed. "web" is built from apps/client.
IMAGES := api-gateway entity-service track-service search-service link-analysis-service alert-service ingest-service analytics-service web
REGISTRY ?=
TAG ?= $(shell git rev-parse --short HEAD)
PLATFORM ?= linux/amd64

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
	@perl -pi -e 's/^(  )([A-Z][A-Z_0-9]*) = "[A-Z_0-9]+",$$/$$1$$2 = "$$2",/' libs/proto-gen/src/ts/sentinel/v1/*.ts libs/proto-gen/src/ts/sentinel/ingest/v1/*.ts

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

# Needs docker buildx. Foreign-platform builds of the librdkafka services run under
# emulation (QEMU/Rosetta), so each image is built and pushed on its own to keep
# memory down. For a throwaway registry on this machine:
#   docker run -d -p 5005:5000 --name reg registry:2
#   make images-push REGISTRY=localhost:5005
images-push: ## Build IMAGES for PLATFORM (default linux/amd64) and push REGISTRY/sentinel/<name>:TAG one at a time (make images-push REGISTRY=registry.example.com)
	@test -n "$(REGISTRY)" || { echo "REGISTRY is required, e.g. make images-push REGISTRY=registry.example.com"; exit 1; }
	@for img in $(IMAGES); do \
		echo "== $$img ($(PLATFORM)) -> $(REGISTRY)/sentinel/$$img:$(TAG)"; \
		docker buildx build --platform $(PLATFORM) -f apps/$${img/web/client}/Dockerfile \
			-t $(REGISTRY)/sentinel/$$img:$(TAG) --push . || exit 1; \
	done

images-local: ## Build IMAGES for this machine's own platform and load them as sentinel/<name>:TAG, one at a time
	@for img in $(IMAGES); do \
		echo "== $$img (native) -> sentinel/$$img:$(TAG)"; \
		docker buildx build -f apps/$${img/web/client}/Dockerfile \
			-t sentinel/$$img:$(TAG) --load . || exit 1; \
	done

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

# Everything the chart renders must validate (kubeconform skips the CRDs it has no schema for), with
# the defaults, the dev overlay, the optional parts switched on, the data tier alone, the apps
# against an external database and the NetworkPolicies and backups switched off. The pod specs of the
# hardened templates (the gateway, the services, the web client, both ingest Deployments, the schema
# and topics Jobs, the data tier and the backups) must each render runAsNonRoot: true; add a template
# to RESTRICTED when it is hardened too.
CHART := k8s/charts/sentinel
CHART_HOST := sentinel.example.com
CHART_SET := --set ingestService.serviceToken=lint --set global.host=$(CHART_HOST)
DEV_VALUES := k8s/overlays/dev/values.yaml
RESTRICTED := --show-only templates/api-gateway-deployment.yaml --show-only templates/services.yaml --show-only templates/web.yaml \
	--show-only templates/ingest-service-deployment.yaml --show-only templates/ingest-listener-deployment.yaml --show-only templates/db-schema-job.yaml \
	--show-only templates/postgresql.yaml --show-only templates/kafka.yaml --show-only templates/kafka-topics-job.yaml --show-only templates/opensearch.yaml \
	--show-only templates/keycloak.yaml --show-only templates/backup-cronjob.yaml
DATA_TIER_ONLY := --set apiGateway.enabled=false --set ingestService.enabled=false --set ingestListener.enabled=false --set web.enabled=false \
	--set entityService.enabled=false --set trackService.enabled=false --set searchService.enabled=false \
	--set linkAnalysisService.enabled=false --set alertService.enabled=false --set analyticsService.enabled=false
# The optional parts of the application tier: registration mail with a password, the web client's
# map keys, the services' ServiceMonitors and the Ingress's class and annotations.
APPS_OPTIONAL := --set mail.host=smtp.example.com --set mail.username=sentinel --set mail.password=lint \
	--set web.mapKeys.googleMapsApiKey=lint --set appServices.serviceMonitor.enabled=true \
	--set ingress.className=traefik --set ingress.annotations.sentinel-lint=lint
# The optional parts of the NetworkPolicies and backups: the listener's node CIDRs, GeoServer's peers,
# the metrics peer and an rclone upload with a time zone.
NETPOL_BACKUP_OPTIONAL := --set networkPolicy.hostNetworkCIDRs={10.42.0.1/32} --set geoserver.enabled=true \
	--set networkPolicy.geoserver.from[0].ipBlock.cidr=192.0.2.0/24 --set ingestService.serviceMonitor.enabled=true \
	--set backup.rclone.enabled=true --set backup.rclone.existingSecret=lint --set backup.rclone.remote=lint:sentinel \
	--set backup.timeZone=Etc/UTC

# The Keycloak realm the chart renders from the dev realm file: no dev user (only the service account
# and sentinel-admin), no -dev secret, every secret a ${ENV} placeholder (the SMTP password too, when
# mail has one), and sentinel-web's redirect URIs and web origins on https://$(CHART_HOST) only.
define chart_realm_check
	@realm="$$(helm template sentinel $(CHART) $(CHART_SET) $(1) --show-only templates/keycloak.yaml | sed -n '/^  sentinel-realm.json: |$$/,/^---$$/p')"; \
	test -n "$$realm" || { echo "chart-lint: no realm rendered ($(1))"; exit 1; }; \
	users=$$(printf '%s\n' "$$realm" | sed -nE 's/^ *"username": "([^"]*)".*/\1/p' | sort | tr '\n' ' '); \
	test "$$users" = "sentinel-admin service-account-sentinel-service " || { echo "chart-lint: realm users are $$users"; exit 1; }; \
	! printf '%s\n' "$$realm" | grep -n -- '-dev"' || { echo "chart-lint: the realm holds a -dev value"; exit 1; }; \
	printf '%s\n' "$$realm" | grep -q '"secret": "$${SENTINEL_SERVICE_CLIENT_SECRET}"' || { echo "chart-lint: sentinel-service's secret is not the placeholder"; exit 1; }; \
	printf '%s\n' "$$realm" | grep -q '"value": "$${SENTINEL_ADMIN_PASSWORD}"' || { echo "chart-lint: sentinel-admin's password is not the placeholder"; exit 1; }; \
	! printf '%s\n' "$$realm" | grep -n '"password": "[^$$]' || { echo "chart-lint: the realm holds a password"; exit 1; }; \
	uris=$$(printf '%s\n' "$$realm" | awk '/"(redirectUris|webOrigins)": \[/ {on=1; next} on && /\]/ {on=0} on'); \
	test -n "$$uris" || { echo "chart-lint: sentinel-web has no redirect URIs"; exit 1; }; \
	! printf '%s\n' "$$uris" | grep -vE '^ *"https://$(subst .,\.,$(CHART_HOST))(/\*)?",?$$' || { echo "chart-lint: a redirect URI or web origin is not on https://$(CHART_HOST)"; exit 1; }
endef

# The NetworkPolicy named sentinel-<component> from a render on stdin, as an awk program.
chart_policy = awk '/^kind: NetworkPolicy$$/ {np=1} /^---/ {np=0; p=0} np && /name: sentinel-$(1)$$/ {p=1} p'

# kubeconform on one render. The render goes to a variable first, so a failing helm template fails
# the target (a pipe would hide it), and a render with no resources is an error too.
define chart_validate
	@out="$$(helm template sentinel $(CHART) $(CHART_SET) $(1))" || { echo "chart-lint: helm template failed ($(1))"; exit 1; }; \
	test "$$(printf '%s\n' "$$out" | grep -c '^kind:')" -gt 0 || { echo "chart-lint: helm template rendered nothing ($(1))"; exit 1; }; \
	printf '%s\n' "$$out" | kubeconform -strict -ignore-missing-schemas -summary
endef

chart-lint: ## Lint the Helm chart, check that it renders db/schema, the topics script, the initdb SQL, a production Keycloak realm, runAsNonRoot in every hardened pod spec and the listener's NetworkPolicy rule, and validate the output with kubeconform (needs helm and kubeconform)
	helm lint $(CHART) $(CHART_SET)
	helm template sentinel $(CHART) $(CHART_SET) | grep -q '^  70_feeds\.sql: '
	helm template sentinel $(CHART) $(CHART_SET) | grep -q 'observations\.raw|24|'
	helm template sentinel $(CHART) $(CHART_SET) | grep -q 'CREATE EXTENSION IF NOT EXISTS postgis_topology;'
	$(call chart_realm_check,)
	$(call chart_realm_check,$(APPS_OPTIONAL))
	@restricted="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestListener.enabled=true $(RESTRICTED))"; \
	pods=$$(printf '%s\n' "$$restricted" | grep -cE '^ +containers:'); \
	roots=$$(printf '%s\n' "$$restricted" | grep -c 'runAsNonRoot: true'); \
	test "$$pods" -gt 0 && test "$$pods" = "$$roots" || { echo "chart-lint: $$roots runAsNonRoot: true for $$pods hardened pod specs"; exit 1; }
	$(call chart_validate,)
	$(call chart_validate,-f $(DEV_VALUES))
	$(call chart_validate,--set geoserver.enabled=true --set ingestService.serviceMonitor.enabled=true)
	$(call chart_validate,$(APPS_OPTIONAL))
	$(call chart_validate,$(DATA_TIER_ONLY))
	$(call chart_validate,--set postgresql.existingSecret=sentinel-db --set postgresql.enabled=false --set kafka.enabled=false --set opensearch.enabled=false)
	$(call chart_validate,$(NETPOL_BACKUP_OPTIONAL))
	$(call chart_validate,--set networkPolicy.enabled=false --set backup.enabled=false)
	@# The listener's rule: with the listener on and no CIDRs PostgreSQL has no from (open in the
	@# cluster); with CIDRs they reach both the PostgreSQL and the Kafka policy.
	@np="$$(helm template sentinel $(CHART) $(CHART_SET) --show-only templates/networkpolicies.yaml)"; \
	pg=$$(printf '%s\n' "$$np" | $(call chart_policy,postgresql)); \
	printf '%s\n' "$$pg" | grep -q 'port: postgresql' || { echo "chart-lint: no PostgreSQL NetworkPolicy rendered"; exit 1; }; \
	! printf '%s\n' "$$pg" | grep -q 'from:' || { echo "chart-lint: PostgreSQL is restricted although the listener has no hostNetworkCIDRs"; exit 1; }; \
	np="$$(helm template sentinel $(CHART) $(CHART_SET) $(NETPOL_BACKUP_OPTIONAL) --show-only templates/networkpolicies.yaml)"; \
	test "$$(printf '%s\n' "$$np" | grep -c 'cidr: "10.42.0.1/32"')" = 2 || { echo "chart-lint: hostNetworkCIDRs do not reach the PostgreSQL and Kafka policies"; exit 1; }
	@# Policies match the pod's port, so they name the container ports: a changed Service port must
	@# leave the web policy on the container's http port (4200) and no policy with a numeric port.
	@ports="--set web.service.port=80 --set ingestService.service.port=8080"; \
	np="$$(helm template sentinel $(CHART) $(CHART_SET) $$ports --show-only templates/networkpolicies.yaml)"; \
	! printf '%s\n' "$$np" | grep -nE '^ +- port: [0-9]' || { echo "chart-lint: a NetworkPolicy names a numeric port"; exit 1; }; \
	printf '%s\n' "$$np" | $(call chart_policy,web) | grep -qE 'port: http$$' || { echo "chart-lint: the web NetworkPolicy does not allow its http port"; exit 1; }; \
	helm template sentinel $(CHART) $(CHART_SET) $$ports --show-only templates/web.yaml | grep -A1 -- '- name: http' | grep -q 'containerPort: 4200' || { echo "chart-lint: the web container's http port is not 4200"; exit 1; }

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
