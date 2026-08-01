#!/bin/bash

# Integration test runner for containerized environments
# This script handles the network connectivity issues when running tests
# from inside a Docker container (like Cursor dev container)

set -euo pipefail

APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "🐳 Integration Test Runner"
echo "=========================="
echo ""

# Check if docker compose services are running
if ! docker ps | grep -q "tests-postgres-test-1"; then
    echo "📦 Starting test infrastructure..."
    cd "$APP_ROOT" && docker compose -f tests/compose.test.yaml up -d
    echo "⏳ Waiting for services to be healthy..."
    sleep 8
fi

# Get container IPs
POSTGRES_IP=$(docker inspect tests-postgres-test-1 --format='{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
MAILPIT_IP=$(docker inspect tests-mailpit-1 --format='{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "📊 PostgreSQL container IP: $POSTGRES_IP"
echo "📬 Mailpit container IP: $MAILPIT_IP"

# Export environment variables for tests
export TEST_DB_HOST="$POSTGRES_IP"
export TEST_DB_PORT="5432"
export TEST_DB_USER="notifuse_test"
export TEST_DB_PASSWORD="test_password"
export TEST_SMTP_HOST="$MAILPIT_IP"
export ENVIRONMENT="test"
export INTEGRATION_TESTS="true"

echo "🔧 Test configuration:"
echo "   DB Host: $TEST_DB_HOST"
echo "   DB Port: $TEST_DB_PORT"
echo "   DB User: $TEST_DB_USER"
echo "   SMTP Host: $TEST_SMTP_HOST"
echo ""

# Run the specified test or all integration tests
TEST_NAME="${1:-TestSetupWizardSigninImmediatelyAfterCompletion}"

echo "🧪 Running test: $TEST_NAME"
echo ""

cd "$APP_ROOT"
go test -race -v ./tests/integration -run "$TEST_NAME" -timeout 30m

TEST_EXIT_CODE=$?

echo ""
if [ $TEST_EXIT_CODE -eq 0 ]; then
    echo "✅ Tests passed!"
else
    echo "❌ Tests failed with exit code: $TEST_EXIT_CODE"
fi

exit $TEST_EXIT_CODE
