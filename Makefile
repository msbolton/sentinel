.PHONY: images-push images-local help proto proto-check proto-test build test lint docker-up docker-down docker-infra clean db-schema db-seed chart-lint deploy backup-now

SHELL := /bin/bash

# Go services
GO_SERVICES := api-gateway ingest-service entity-service track-service search-service link-analysis-service alert-service analytics-service sensor-sim
# Go programs that live in simulators/ (everything else in GO_SERVICES is in
# apps/). SVC_DIR expands, inside a recipe loop over $$svc, to the folder.
SIMULATORS := sensor-sim
SVC_DIR = $$([[ " $(SIMULATORS) " == *" $$svc "* ]] && echo simulators || echo apps)

# Container images (the names the Helm chart pulls as sentinel/<name>). "web"
# is built from apps/client, sensor-sim (the chart's sensorSim, for the demo)
# from simulators/sensor-sim, the rest from apps/<name>. IMAGE_DOCKERFILE
# expands, inside a recipe loop over $$img, to the image's Dockerfile.
IMAGES := api-gateway entity-service track-service search-service link-analysis-service alert-service ingest-service analytics-service web sensor-sim
IMAGE_DOCKERFILE = $$([[ " $(SIMULATORS) " == *" $$img "* ]] && echo simulators/$$img || echo apps/$${img/web/client})/Dockerfile
# REGISTRY and TAG are shared by images-push and deploy, so a deploy from the
# same commit pulls the images that commit pushed.
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
		cd $(SVC_DIR)/$$svc && go mod tidy && cd ../..; \
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
		docker buildx build --platform $(PLATFORM) -f $(IMAGE_DOCKERFILE) \
			-t $(REGISTRY)/sentinel/$$img:$(TAG) --push . || exit 1; \
	done

images-local: ## Build IMAGES for this machine's own platform and load them as sentinel/<name>:TAG, one at a time
	@for img in $(IMAGES); do \
		echo "== $$img (native) -> sentinel/$$img:$(TAG)"; \
		docker buildx build -f $(IMAGE_DOCKERFILE) \
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
# the defaults, the dev overlay, the prod overlay alone and with the local (k3d) one over it, the
# optional parts switched on, the data tier alone, the apps
# against an external database and the NetworkPolicies and backups switched off. The pod specs of the
# hardened templates (the gateway, the services, the web client, both ingest Deployments, the schema
# and topics Jobs, the data tier, the backups and the sensor simulators) must each render
# runAsNonRoot: true, in a render with the listener, the simulators and the gateway's GeoIP
# initContainer on; add a template to RESTRICTED when it is hardened too.
CHART := k8s/charts/sentinel
CHART_HOST := sentinel.example.com
CHART_SET := --set ingestService.serviceToken=lint --set global.host=$(CHART_HOST)
DEV_VALUES := k8s/overlays/dev/values.yaml
PROD_VALUES := k8s/overlays/prod/values.yaml
LOCAL_VALUES := k8s/overlays/local/values.yaml
RESTRICTED := --show-only templates/api-gateway-deployment.yaml --show-only templates/services.yaml --show-only templates/web.yaml \
	--show-only templates/ingest-service-deployment.yaml --show-only templates/ingest-listener-deployment.yaml --show-only templates/db-schema-job.yaml \
	--show-only templates/postgresql.yaml --show-only templates/kafka.yaml --show-only templates/kafka-topics-job.yaml --show-only templates/opensearch.yaml \
	--show-only templates/keycloak.yaml --show-only templates/backup-cronjob.yaml --show-only templates/sensor-sim.yaml
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

# The public demo with everything on: the simulators, the gateway's DEMO_* settings and its GeoIP download.
DEMO_ON := --set sensorSim.enabled=true --set sensorSim.tokenSecret=lint --set apiGateway.demo.enabled=true \
	--set apiGateway.demo.tokenSecret=lint --set apiGateway.demo.geoip.enabled=true --set apiGateway.demo.geoip.existingSecret=lint

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

chart-lint: ## Lint the Helm chart, check that it renders db/schema, the topics script, the initdb SQL, a production Keycloak realm, runAsNonRoot in every hardened pod spec, the listener's NetworkPolicy rule, the prod overlay's reserved ports and the demo's simulators and gateway settings, and validate the output, the overlays' too, with kubeconform (needs helm and kubeconform)
	helm lint $(CHART) $(CHART_SET)
	helm template sentinel $(CHART) $(CHART_SET) | grep -q '^  70_feeds\.sql: '
	helm template sentinel $(CHART) $(CHART_SET) | grep -q 'observations\.raw|24|'
	helm template sentinel $(CHART) $(CHART_SET) | grep -q 'CREATE EXTENSION IF NOT EXISTS postgis_topology;'
	$(call chart_realm_check,)
	$(call chart_realm_check,$(APPS_OPTIONAL))
	@restricted="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestListener.enabled=true $(DEMO_ON) $(RESTRICTED))"; \
	pods=$$(printf '%s\n' "$$restricted" | grep -cE '^ +containers:'); \
	roots=$$(printf '%s\n' "$$restricted" | grep -c 'runAsNonRoot: true'); \
	test "$$pods" -gt 0 && test "$$pods" = "$$roots" || { echo "chart-lint: $$roots runAsNonRoot: true for $$pods hardened pod specs"; exit 1; }
	$(call chart_validate,)
	$(call chart_validate,-f $(DEV_VALUES))
	$(call chart_validate,-f $(PROD_VALUES))
	$(call chart_validate,-f $(PROD_VALUES) -f $(LOCAL_VALUES))
	$(call chart_validate,--set geoserver.enabled=true --set ingestService.serviceMonitor.enabled=true)
	$(call chart_validate,$(APPS_OPTIONAL))
	$(call chart_validate,$(DATA_TIER_ONLY))
	$(call chart_validate,--set postgresql.existingSecret=sentinel-db --set postgresql.enabled=false --set kafka.enabled=false --set opensearch.enabled=false)
	$(call chart_validate,$(NETPOL_BACKUP_OPTIONAL))
	$(call chart_validate,$(DEMO_ON))
	$(call chart_validate,-f $(PROD_VALUES) $(DEMO_ON))
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
	@# The prod overlay's INGEST_RESERVED_PORTS replaces the ingest service's default list, so it must
	@# still hold every entry of that list (registry.DefaultReservedPorts), in both ingest Deployments.
	@defaults=$$(sed -nE 's/^const DefaultReservedPorts = "([^"]*)"$$/\1/p' apps/ingest-service/internal/registry/ports.go); \
	test -n "$$defaults" || { echo "chart-lint: DefaultReservedPorts not found in apps/ingest-service/internal/registry/ports.go"; exit 1; }; \
	out="$$(helm template sentinel $(CHART) $(CHART_SET) -f $(PROD_VALUES) --show-only templates/ingest-service-deployment.yaml --show-only templates/ingest-listener-deployment.yaml)"; \
	vals=$$(printf '%s\n' "$$out" | grep -A1 'name: INGEST_RESERVED_PORTS' | sed -nE 's/^ *value: "([^"]*)"$$/\1/p'); \
	test "$$(printf '%s\n' "$$vals" | grep -c .)" = 2 || { echo "chart-lint: the prod overlay does not set INGEST_RESERVED_PORTS in both ingest Deployments"; exit 1; }; \
	for v in $$vals; do for p in $${defaults//,/ }; do \
		case ",$$v," in *",$$p,"*) ;; *) echo "chart-lint: the prod overlay's INGEST_RESERVED_PORTS lacks $$p of the default list"; exit 1;; esac; \
	done; done
	@# Terrain is on while INGEST_TERRAIN_CACHE_DIR is set: both ingest Deployments carry it, with
	@# its mounted volume, when ingestService.terrain.enabled, and neither when it is not.
	@for tpl in ingest-service ingest-listener; do \
		on="$$(helm template sentinel $(CHART) $(CHART_SET) --show-only templates/$$tpl-deployment.yaml)"; \
		printf '%s\n' "$$on" | grep -q 'name: INGEST_TERRAIN_CACHE_DIR' || { echo "chart-lint: $$tpl has no INGEST_TERRAIN_CACHE_DIR with terrain enabled"; exit 1; }; \
		printf '%s\n' "$$on" | grep -q 'mountPath: /var/cache/sentinel/terrain' || { echo "chart-lint: $$tpl does not mount the terrain cache"; exit 1; }; \
		printf '%s\n' "$$on" | grep -A3 'name: terrain-cache$$' | grep -q 'emptyDir:' || { echo "chart-lint: $$tpl's terrain cache is not an emptyDir"; exit 1; }; \
		claim="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestService.terrain.existingClaim=terrain-pvc --show-only templates/$$tpl-deployment.yaml)"; \
		printf '%s\n' "$$claim" | grep -q 'claimName: terrain-pvc' || { echo "chart-lint: $$tpl ignores ingestService.terrain.existingClaim"; exit 1; }; \
		off="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestService.terrain.enabled=false --show-only templates/$$tpl-deployment.yaml)"; \
		! printf '%s\n' "$$off" | grep -q 'INGEST_TERRAIN\|terrain-cache\|GOMEMLIMIT' || { echo "chart-lint: $$tpl still has terrain settings with terrain disabled"; exit 1; }; \
		printf '%s\n' "$$on" | grep -A1 'name: INGEST_TERRAIN_MEM_TILES$$' | grep -q 'value: "4"' || { echo "chart-lint: $$tpl has no INGEST_TERRAIN_MEM_TILES of 4"; exit 1; }; \
		printf '%s\n' "$$on" | grep -A1 'name: GOMEMLIMIT$$' | grep -q 'value: "819MiB"' || { echo "chart-lint: $$tpl's GOMEMLIMIT is not 80% of its 1Gi limit"; exit 1; }; \
		mem="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestService.terrain.goMemLimit=700MiB --show-only templates/$$tpl-deployment.yaml)"; \
		printf '%s\n' "$$mem" | grep -A1 'name: GOMEMLIMIT$$' | grep -q 'value: "700MiB"' || { echo "chart-lint: $$tpl ignores ingestService.terrain.goMemLimit"; exit 1; }; \
	done
	@# GOMEMLIMIT is 80% of a fractional limit too (1.5Gi is 1536 MiB), never "0MiB".
	@frac="$$(helm template sentinel $(CHART) $(CHART_SET) --set ingestService.resources.limits.memory=1.5Gi --show-only templates/ingest-service-deployment.yaml)"; \
	printf '%s\n' "$$frac" | grep -A1 'name: GOMEMLIMIT$$' | grep -q 'value: "1228MiB"' || { echo "chart-lint: ingest-service's GOMEMLIMIT is not 80% of a 1.5Gi limit (1228MiB)"; exit 1; }
	@# The public demo is off by default: no simulator, no DEMO_* and no GeoIP download.
	@off="$$(helm template sentinel $(CHART) $(CHART_SET))" || exit 1; \
	! printf '%s\n' "$$off" | grep -q 'sensor-sim' || { echo "chart-lint: the defaults render a sensor simulator"; exit 1; }; \
	! printf '%s\n' "$$off" | grep -q 'name: DEMO_' || { echo "chart-lint: the defaults render DEMO_* settings"; exit 1; }; \
	! printf '%s\n' "$$off" | grep -qE -- '- name: geoip$$|/var/lib/geoip' || { echo "chart-lint: the defaults render the GeoIP download"; exit 1; }
	@# One simulator Deployment per kind, each sending to its node's address on sensorSim.port.
	@sims="$$(helm template sentinel $(CHART) $(CHART_SET) --set sensorSim.enabled=true --set sensorSim.tokenSecret=t)" || { echo "chart-lint: the simulators do not render"; exit 1; }; \
	names=$$(printf '%s\n' "$$sims" | awk '/^---/ {d=0} /^kind: Deployment$$/ {d=1} d && /^  name: / {print $$2}' | grep -- '-sensor-sim-' | sort | tr '\n' ' '); \
	test "$$names" = "sentinel-sensor-sim-eoir sentinel-sensor-sim-esm sentinel-sensor-sim-radar " || { echo "chart-lint: the simulator Deployments are $$names"; exit 1; }; \
	test "$$(printf '%s\n' "$$sims" | grep -A1 'name: SENSOR_SIM_ADDR$$' | grep -cF 'value: "$$(HOST_IP):6201"')" = 3 || { echo "chart-lint: a simulator does not send to \$$(HOST_IP):6201"; exit 1; }; \
	! helm template sentinel $(CHART) $(CHART_SET) --set sensorSim.enabled=true >/dev/null 2>&1 || { echo "chart-lint: the simulators render without sensorSim.tokenSecret"; exit 1; }
	@# The gateway's demo settings, its ingest address on its node and the GeoIP download only with geoip on.
	@demo="$$(helm template sentinel $(CHART) $(CHART_SET) --set apiGateway.demo.enabled=true --set apiGateway.demo.tokenSecret=t --show-only templates/api-gateway-deployment.yaml)" || { echo "chart-lint: the demo gateway does not render"; exit 1; }; \
	printf '%s\n' "$$demo" | grep -A1 'name: DEMO_ENABLED$$' | grep -q 'value: "true"' || { echo "chart-lint: the gateway has no DEMO_ENABLED of \"true\""; exit 1; }; \
	printf '%s\n' "$$demo" | grep -A1 'name: DEMO_INGEST_ADDR$$' | grep -qF 'value: "$$(HOST_IP):6202"' || { echo "chart-lint: DEMO_INGEST_ADDR is not \$$(HOST_IP):6202"; exit 1; }; \
	! printf '%s\n' "$$demo" | grep -q 'initContainers:\|DEMO_GEOIP_DB' || { echo "chart-lint: the gateway downloads GeoIP with geoip off"; exit 1; }; \
	geo="$$(helm template sentinel $(CHART) $(CHART_SET) --set apiGateway.demo.enabled=true --set apiGateway.demo.tokenSecret=t --set apiGateway.demo.geoip.enabled=true --set apiGateway.demo.geoip.existingSecret=t --show-only templates/api-gateway-deployment.yaml)" || { echo "chart-lint: the demo gateway with geoip does not render"; exit 1; }; \
	printf '%s\n' "$$geo" | sed -n '/^      initContainers:$$/,/^      containers:$$/p' | grep -q -- '- name: geoip$$' || { echo "chart-lint: the gateway has no geoip initContainer"; exit 1; }; \
	printf '%s\n' "$$geo" | grep -A1 'name: DEMO_GEOIP_DB$$' | grep -qF 'value: "/var/lib/geoip/GeoLite2-City.mmdb"' || { echo "chart-lint: DEMO_GEOIP_DB is not /var/lib/geoip/GeoLite2-City.mmdb"; exit 1; }; \
	printf '%s\n' "$$geo" | grep -A2 'mountPath: /var/lib/geoip$$' | grep -q 'readOnly: true' || { echo "chart-lint: the gateway does not mount /var/lib/geoip read-only"; exit 1; }; \
	! helm template sentinel $(CHART) $(CHART_SET) --set apiGateway.demo.enabled=true --set apiGateway.demo.tokenSecret=t --set apiGateway.extraEnv.DEMO_ENABLED=true >/dev/null 2>&1 || { echo "chart-lint: apiGateway.extraEnv may set DEMO_ENABLED"; exit 1; }

# ----------------------------------------------------------------------------
# Deployment (docs/deployment.md)
# ----------------------------------------------------------------------------

# make deploy installs or upgrades the release with the prod overlay, then VALUES (your site file, or
# several, space-separated), with the images of REGISTRY and TAG: build and push them first with
# make images-push from the same commit. No --wait and no rollback on failure: on a first install the
# schema and topics Jobs run after the release's resources, and the services crash-loop until they
# have. HELM_ARGS adds flags (HELM_ARGS="--wait --rollback-on-failure" for an upgrade, say). For k3d:
#   make deploy REGISTRY=docker.io VALUES=k8s/overlays/local/values.yaml
RELEASE ?= sentinel
NAMESPACE ?= sentinel
VALUES ?=
HELM_ARGS ?=

deploy: ## helm upgrade --install RELEASE (sentinel) into NAMESPACE (sentinel) with the prod overlay, VALUES and the REGISTRY/TAG images (make deploy REGISTRY=registry.example.com VALUES=site.yaml)
	@test -n "$(REGISTRY)" || { echo "REGISTRY is required, e.g. make deploy REGISTRY=registry.example.com VALUES=site.yaml (REGISTRY=docker.io for images imported into k3d)"; exit 1; }
	helm upgrade --install $(RELEASE) $(CHART) -n $(NAMESPACE) --create-namespace \
		-f $(PROD_VALUES) $(foreach v,$(VALUES),-f $(v)) \
		--set global.imageRegistry=$(REGISTRY) --set global.imageTag=$(TAG) --timeout 20m $(HELM_ARGS)

# How long backup-now waits for its Job, in seconds: a little over the Job's own deadline
# (backup.activeDeadlineSeconds, 3600), after which Kubernetes marks it Failed anyway.
BACKUP_TIMEOUT ?= 3700

backup-now: ## Run the release's backup CronJob now as a Job, wait for it (BACKUP_TIMEOUT seconds) and print its log (RELEASE, NAMESPACE)
	@cj=$$(kubectl -n $(NAMESPACE) get cronjob -l app.kubernetes.io/instance=$(RELEASE),app.kubernetes.io/name=backup -o name) || exit 1; \
	test -n "$$cj" || { echo "no backup CronJob for release $(RELEASE) in namespace $(NAMESPACE)"; exit 1; }; \
	job=backup-now-$$(date -u +%Y%m%d%H%M%S); \
	kubectl -n $(NAMESPACE) create job --from=$$cj $$job || exit 1; \
	echo "waiting for job/$$job (up to $(BACKUP_TIMEOUT)s) ..."; \
	deadline=$$(( $$(date +%s) + $(BACKUP_TIMEOUT) )); errors=0; state=; \
	while :; do \
		if state=$$(kubectl -n $(NAMESPACE) get job $$job -o jsonpath='{.status.conditions[?(@.status=="True")].type}'); then errors=0; \
		else errors=$$((errors + 1)); test $$errors -lt 6 || { echo "job/$$job: kubectl get failed $$errors times in a row"; exit 1; }; fi; \
		case " $$state " in *" Complete "*|*" Failed "*) break;; esac; \
		test $$(date +%s) -lt $$deadline || { echo "job/$$job: not finished after $(BACKUP_TIMEOUT)s"; \
			kubectl -n $(NAMESPACE) logs job/$$job --all-containers --prefix; exit 1; }; \
		sleep 5; \
	done; \
	kubectl -n $(NAMESPACE) logs job/$$job --all-containers --prefix; \
	case " $$state " in *" Complete "*) echo "job/$$job: Complete";; *) echo "job/$$job: Failed"; exit 1;; esac

# ----------------------------------------------------------------------------
# Cleanup
# ----------------------------------------------------------------------------

clean: ## Clean all build artifacts
	rm -rf dist/ node_modules/.cache
	@for svc in $(GO_SERVICES); do \
		cd $(SVC_DIR)/$$svc && go clean && cd ../..; \
	done
	cd libs/common-go && go clean
	find . -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true
