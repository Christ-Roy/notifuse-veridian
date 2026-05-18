.PHONY: build test-unit run clean keygen test-service test-repo test-http test-migrations test-database test-pkg dev coverage coverage-report docker-build docker-run docker-stop docker-clean docker-logs docker-buildx-setup docker-publish docker-compose-up docker-compose-down docker-compose-build openapi-bundle openapi-lint openapi-preview demo-hmac setup-hooks check-test-mapping

build:
	@echo "Building with CGO enabled (required for V8)..."
	CGO_ENABLED=1 go build -o bin/server ./cmd/api

build-static:
	@echo "Note: Static builds (CGO_ENABLED=0) are not compatible with V8"
	@echo "Use 'make build' for local development or Docker for deployment"

test-unit:
	go test -race -v ./internal/... ./pkg/...

# End-to-end test command for Cursor Agent: runs all integration tests (non-verbose)
e2e-test-within-cursor-agent:
	@echo "Running all integration tests (non-verbose)..."
	@./run-integration-tests.sh "Test" 2>&1 | grep -E "PASS|FAIL|^ok|===|^---" || true
	@echo "\n✅ All integration tests completed"

test-integration:
	INTEGRATION_TESTS=true go test -race -timeout 9m ./tests/integration/ -v

test-domain:
	go test -race -v ./internal/domain

test-service:
	go test -race -v ./internal/service ./internal/service/broadcast

test-repo:
	go test -race -v ./internal/repository

test-http:
	go test -race -v ./internal/http

test-migrations:
	go test -race -v ./internal/migrations

test-database:
	go test -race -v ./internal/database ./internal/database/schema

test-pkg:
	go test -race -v ./pkg/...

# Comprehensive test coverage command
coverage:
	@echo "Running comprehensive tests and generating coverage report..."
	@go test -race -coverprofile=coverage.out -covermode=atomic $$(go list ./... | grep -v '/tests/integration') -v
	@echo "\n=== Comprehensive Test Coverage Summary ==="
	@go tool cover -func=coverage.out | grep total
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Detailed HTML coverage report generated: coverage.html"

# Per-file coverage report for /internal and /pkg directories
coverage-report:
	@./scripts/coverage-report.sh $(THRESHOLD)

run:
	go run ./cmd/api

dev:
	@command -v air >/dev/null 2>&1 || { echo "❌ air manquant — installe avec: go install github.com/air-verse/air@latest"; exit 1; }
	air

# === Veridian dev env (cycle hot-reload 2-3s pour itérer plus vite que la CI)
# Usage premier setup :
#   make dev-bootstrap     # check tools + .env + keygen + compose test
#   make dev-up            # start Postgres test + Mailpit (background)
#   make dev               # Air hot-reload
#   make dev-tailscale     # bonus : tunnel HTTPS public via Tailscale
#   make dev-stop          # stop tout
.PHONY: dev-bootstrap dev-up dev-stop dev-tailscale dev-status

# Vérifie pré-requis + crée .env depuis l'example + génère PASETO keys
dev-bootstrap:
	@echo "→ Checking tools..."
	@command -v docker >/dev/null 2>&1 || { echo "❌ docker missing"; exit 1; }
	@command -v go >/dev/null 2>&1 || { echo "❌ go missing"; exit 1; }
	@command -v air >/dev/null 2>&1 || { echo "→ Installing air..."; go install github.com/air-verse/air@latest; }
	@if [ ! -f .env ]; then \
		echo "→ Copying .env.dev.example to .env..."; \
		cp .env.dev.example .env; \
		echo "→ Generating PASETO keys (overwrite empty placeholders)..."; \
		./scripts/dev/inject-paseto-keys.sh; \
		echo "→ Injecting secrets from ~/credentials/.all-creds.env..."; \
		./scripts/dev/inject-secrets.sh; \
		echo "✓ .env ready. Review it, then run 'make dev-up && make dev'."; \
	else \
		echo "✓ .env already exists (skip)"; \
	fi

# Start Postgres test (:5433) + Mailpit SMTP (:1025) + UI (:8025) — background
dev-up:
	@echo "→ Starting Postgres test + Mailpit..."
	docker compose -f tests/compose.test.yaml up -d
	@echo "→ Waiting for healthy..."
	@for i in $$(seq 1 30); do \
		if docker compose -f tests/compose.test.yaml ps --format json 2>/dev/null | grep -q '"Health":"healthy"'; then \
			echo "✓ Services healthy"; \
			docker compose -f tests/compose.test.yaml ps; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "⚠ Services not healthy after 30s, check 'docker compose -f tests/compose.test.yaml logs'"

# Stop tous les services dev
dev-stop:
	@docker compose -f tests/compose.test.yaml down
	@pkill -f "tailscale serve" 2>/dev/null || true
	@echo "✓ Dev services stopped"

# Expose le serveur local (port 8080) en HTTPS public via Tailscale Funnel.
# URL stable, valable tant que ta machine est en ligne. Idéal pour tester depuis
# une autre machine ou contre le Hub local.
dev-tailscale:
	@command -v tailscale >/dev/null 2>&1 || { echo "❌ tailscale missing"; exit 1; }
	@echo "→ Exposing http://localhost:8080 via Tailscale..."
	@tailscale serve --bg --https=443 http://localhost:8080
	@tailscale serve status
	@echo ""
	@echo "✓ Public URL : https://$$(tailscale status --json | jq -r '.Self.DNSName' | sed 's/\.$$//')"

# Affiche l'état complet du dev env
dev-status:
	@echo "=== Docker services ==="
	@docker compose -f tests/compose.test.yaml ps 2>/dev/null || echo "Not started"
	@echo ""
	@echo "=== Air process ==="
	@pgrep -af "^air" | head -3 || echo "Air not running"
	@echo ""
	@echo "=== Tailscale serve ==="
	@tailscale serve status 2>/dev/null || echo "Not exposed"
	@echo ""
	@echo "=== Local health check ==="
	@curl -s -o /dev/null -w "http://localhost:8080/healthz → HTTP %{http_code}\n" http://localhost:8080/healthz 2>&1 || echo "Server not up"

clean:
	rm -rf bin/ tmp/ coverage.out coverage.html coverage-internal-pkg.out coverage-report.txt

keygen:
	go run cmd/keygen/main.go

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t notifuse:latest .

docker-run:
	@echo "Running Docker container..."
	docker run -d --name notifuse \
		-p 8080:8080 \
		-e SECRET_KEY=$${SECRET_KEY} \
		-e ROOT_EMAIL=$${ROOT_EMAIL:-admin@example.com} \
		-e API_ENDPOINT=$${API_ENDPOINT:-http://localhost:8080} \
		-e WEBHOOK_ENDPOINT=$${WEBHOOK_ENDPOINT:-http://localhost:8080} \
		notifuse:latest

docker-stop:
	@echo "Stopping Docker container..."
	docker stop notifuse || true
	docker rm notifuse || true

docker-clean: docker-stop
	@echo "Removing Docker image..."
	docker rmi notifuse:latest || true

docker-logs:
	@echo "Showing Docker container logs..."
	docker logs -f notifuse

docker-buildx-setup:
	@echo "Setting up Docker buildx for multi-platform builds..."
	@docker buildx create --name notifuse-builder --use --bootstrap 2>/dev/null || \
		docker buildx use notifuse-builder 2>/dev/null || \
		echo "Buildx builder already exists and is active"
	@docker buildx inspect --bootstrap

docker-publish:
	@echo "Building and publishing multi-platform Docker image to Docker Hub..."
	@if [ -z "$(word 2,$(MAKECMDGOALS))" ]; then \
		echo "Building with tag: latest for amd64 and arm64"; \
		docker buildx build --platform linux/amd64,linux/arm64 -t notifuse/notifuse:latest --push .; \
	else \
		echo "Building with tag: $(word 2,$(MAKECMDGOALS)) for amd64 and arm64"; \
		docker buildx build --platform linux/amd64,linux/arm64 -t notifuse/notifuse:$(word 2,$(MAKECMDGOALS)) --push .; \
	fi

# This prevents make from trying to run the tag as a target
%:
	@:

# Docker compose commands
docker-compose-up:
	@echo "Starting services with Docker Compose..."
	docker compose up -d

docker-compose-down:
	@echo "Stopping services with Docker Compose..."
	docker compose down

docker-compose-build:
	@echo "Building services with Docker Compose..."
	docker compose build

# OpenAPI commands
openapi-bundle:
	@echo "Bundling OpenAPI spec from YAML chunks..."
	@npx @redocly/cli bundle openapi/openapi.yaml -o openapi.json --ext json
	@echo "OpenAPI spec bundled to openapi.json"

openapi-lint:
	@echo "Linting OpenAPI spec..."
	@npx @redocly/cli lint openapi/openapi.yaml

openapi-preview:
	@echo "Starting OpenAPI preview server..."
	@npx @redocly/cli preview-docs openapi/openapi.yaml

# Generate HMAC for demo reset endpoint
# Usage: make demo-hmac ROOT_EMAIL=your@email.com SECRET_KEY=your-secret-key
demo-hmac:
	@if [ -z "$(ROOT_EMAIL)" ] || [ -z "$(SECRET_KEY)" ]; then \
		echo "Usage: make demo-hmac ROOT_EMAIL=your@email.com SECRET_KEY=your-secret-key"; \
		echo ""; \
		echo "This generates the HMAC needed to call the /api/demo.reset endpoint."; \
		exit 1; \
	fi
	@echo "Generating HMAC for demo reset..."
	@go run -exec "" cmd/hmac/main.go "$(ROOT_EMAIL)" "$(SECRET_KEY)"

# === Veridian CI hooks ===

# Install pre-push hook (mapping source ↔ test, Constitution CI §2)
setup-hooks:
	@if [ ! -f .husky/pre-push ]; then \
		echo "❌ .husky/pre-push manquant"; exit 1; \
	fi
	@chmod +x .husky/pre-push scripts/ci/check-test-mapping.sh
	@git config core.hooksPath .husky
	@echo "✓ Hooks installés (core.hooksPath = .husky)"
	@echo "  → pre-push exécutera scripts/ci/check-test-mapping.sh"
	@echo "  → Constitution CI §3 : JAMAIS --no-verify"

# Run le check de mapping en local (working tree)
check-test-mapping:
	@BASE_REF=$${BASE_REF:-origin/main} scripts/ci/check-test-mapping.sh

.DEFAULT_GOAL := build