# Yamata no Orochi - Makefile for testing and development

.PHONY: help test test-models test-repository test-coverage test-clean test-db-check build lint fmt vet clean run run-dev run-debug run-watch swag swag-init swag-clean run-dev-simple migrate migrate-create swagger-ui ci-fmt-check ci-test ci-test-unit ci-build

# Set the shell to bash for consistent behavior
SHELL := /bin/bash

# Default target
help:
	@echo "Available targets:"
	@echo "  run            - Run the application package (loads .env)"
	@echo "  run-dev        - Run in development mode with race detection"
	@echo "  run-debug      - Run with debug information and race detection"
	@echo "  run-watch      - Run with file watching (auto-restart on changes)"
	@echo "  test           - Run all tests"
	@echo "  test-models    - Run models package tests"
	@echo "  test-repository - Run repository package tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  test-clean     - Clean test artifacts"
	@echo "  test-db-check  - Check database connectivity"
	@echo "  build          - Build the application"
	@echo "  lint           - Run linter"
	@echo "  fmt            - Format code"
	@echo "  vet            - Run go vet"
	@echo "  clean          - Clean build artifacts"
	@echo "  swag           - Generate Swagger documentation"
	@echo "  swag-init      - Initialize Swagger documentation (first time)"
	@echo "  swag-clean     - Clean generated Swagger files"
	@echo "  run-dev-simple - Run app in development mode (includes Swagger generation)"
	@echo "  migrate        - Run database migrations"
	@echo "  migrate-create - Create database and run migrations"
	@echo "  swagger-ui     - Open standalone Swagger UI in browser"
	@echo "  ci-test-unit   - Run unit tests that need no database or Redis (CI-safe)"

# Load environment variables from .env if it exists
LOAD_ENV := if [ -f .env ]; then \
				echo "Loading environment variables from .env file..."; \
				set -a; \
				source .env; \
				set +a; \
			else \
				echo "Warning: .env file not found. Using default environment variables."; \
			fi

# Load test environment variables from test.env if it exists
LOAD_TEST_ENV := if [ -f test.env ]; then \
					echo "Loading test environment variables from test.env file..."; \
					source test.env; \
				else \
					echo "Warning: test.env file not found. Using default test environment variables."; \
				fi

# Run the application with go run and load .env file
run:
	@echo "Starting Yamata no Orochi application..."
	@$(LOAD_ENV) && go run .

# Run in development mode with additional flags
run-dev:
	@echo "Starting Yamata no Orochi in development mode..."
	@$(LOAD_ENV) && go run -race .

# Run with debug information
run-debug:
	@echo "Starting Yamata no Orochi with debug information..."
	@$(LOAD_ENV) && go run -race -gcflags='all=-N -l' .

# Run with file watching (requires air)
run-watch:
	@echo "Starting Yamata no Orochi with file watching..."
	@if ! command -v air > /dev/null 2>&1; then \
		echo "Error: air is required for run-watch. Install it with: go install github.com/cosmtrek/air@latest"; \
		exit 1; \
	fi
	@$(LOAD_ENV) && air

# Test targets
test: test-db-check
	@echo "Running all tests..."
	go test -v -race ./tests

test-models: test-db-check
	@echo "Running models tests..."
	go test -v -race -run "TestAccountType|TestCustomer|TestOTP|TestCustomerSession|TestAuditLog|TestModelRelationships" ./tests

test-repository: test-db-check
	@echo "Running repository tests..."
	go test -v -race -run "TestAccountTypeRepository|TestCustomerRepository|TestCustomerSessionRepository|TestAuditLogRepository|TestBaseRepository" ./tests

test-coverage: test-db-check
	@echo "Running tests with coverage..."
	go test -v -race -coverprofile=coverage.out ./tests
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

test-clean:
	@echo "Cleaning test artifacts..."
	rm -f coverage.out coverage.html
	@echo "Test artifacts cleaned"

test-db-check:
	@echo "Checking database connectivity..."
	@if ! pg_isready -h $(TEST_DB_HOST) -p $(TEST_DB_PORT) -U $(TEST_DB_USER) > /dev/null 2>&1; then \
		echo "Error: PostgreSQL is not available at $(TEST_DB_HOST):$(TEST_DB_PORT)"; \
		echo "Please ensure PostgreSQL is running and accessible"; \
		echo "Connection details:"; \
		echo "  Host: $(TEST_DB_HOST)"; \
		echo "  Port: $(TEST_DB_PORT)"; \
		echo "  User: $(TEST_DB_USER)"; \
		exit 1; \
	fi
	@echo "Database connectivity OK"

# Development targets
build:
	@echo "Building application..."
	go build -o bin/yamata-no-orochi .
	@echo "Build complete: bin/yamata-no-orochi"

ci-build:
	@echo "Building CI binary..."
	go build -o bin/yamata-no-orochi-ci .
	@echo "CI build complete: bin/yamata-no-orochi-ci"

lint:
	@echo "Running linter..."
	golangci-lint run

fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted"

ci-fmt-check:
	@echo "Checking Go formatting..."
	@unformatted=$$(find . -path ./vendor -prune -o -name '*.go' -print | xargs gofmt -l); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "Formatting OK"

vet:
	@echo "Running go vet..."
	go vet ./...
	@echo "Go vet complete"

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Parallel test execution (faster for CI)
test-parallel: test-db-check
	@echo "Running tests in parallel..."
	go test -v -race -parallel 4 ./tests

# Verbose test with detailed output
test-verbose: test-db-check
	@echo "Running tests with verbose output..."
	go test -v -race -count=1 ./tests

# Test specific pattern
test-pattern: test-db-check
	@if [ -z "$(PATTERN)" ]; then \
		echo "Usage: make test-pattern PATTERN=<test_pattern>"; \
		echo "Example: make test-pattern PATTERN=TestCustomer"; \
		exit 1; \
	fi
	@echo "Running tests matching pattern: $(PATTERN)"
	go test -v -race -run "$(PATTERN)" ./tests

# Quick test (no race detection, faster)
test-quick:
	@echo "Running quick tests..."
	go test ./tests

ci-test:
	@echo "Running maintained CI test subset..."
	go test ./app/scheduler

ci-test-unit:
	@echo "Running unit tests (no database or Redis required)..."
	go test -race ./app/dto/... ./app/middleware/... ./app/scheduler/... ./business_flow/...

# Test with timeout
test-timeout: test-db-check
	@echo "Running tests with timeout..."
	go test -v -race -timeout 5m ./tests

# Swagger documentation targets
swag: swag-check
	@echo "Generating Swagger documentation..."
	swag init -g main.go -o docs
	@echo "Swagger documentation generated successfully"

swag-init: swag-install
	@echo "Initializing Swagger documentation..."
	swag init -g main.go -o docs
	@echo "Swagger documentation initialized successfully"

swag-clean:
	@echo "Cleaning generated Swagger files..."
	rm -f docs/docs.go docs/swagger.json docs/swagger.yaml
	@echo "Swagger files cleaned"

swag-check:
	@if ! command -v swag > /dev/null 2>&1; then \
		echo "Error: swag is not installed. Installing now..."; \
		go install github.com/swaggo/swag/cmd/swag@latest; \
	fi

swag-install:
	@echo "Installing swag tool..."
	go install github.com/swaggo/swag/cmd/swag@latest
	@echo "swag tool installed successfully"

run-dev-simple:
	@echo "Running in simplest development mode (no Docker)..."
	@echo "Starting development server..."
	@chmod +x scripts/run-dev.sh
	@./scripts/run-dev.sh

# Database migration targets
migrate:
	@echo "Running database migrations..."
	@if [ -z "$(DB_HOST)" ]; then \
		echo "Database configuration not found. Please set environment variables:"; \
		echo "  export DB_HOST=localhost"; \
		echo "  export DB_PORT=5432"; \
		echo "  export DB_USER=postgres"; \
		echo "  export DB_PASSWORD=postgres"; \
		echo "  export DB_NAME=yamata_db"; \
		echo ""; \
		echo "Or use: make migrate-create"; \
		exit 1; \
	fi
	@if ! command -v psql >/dev/null 2>&1; then \
		echo "Error: PostgreSQL client (psql) not found. Please install postgresql-client"; \
		exit 1; \
	fi
	@if ! pg_isready -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) >/dev/null 2>&1; then \
		echo "Error: Cannot connect to PostgreSQL at $(DB_HOST):$(DB_PORT)"; \
		echo "Please ensure PostgreSQL is running"; \
		exit 1; \
	fi
	@psql -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) -d $(DB_NAME) -f migrations/run_all_up.sql
	@echo "Migrations completed successfully"

migrate-create:
	@echo "Creating database and running migrations..."
	@if [ -z "$(DB_HOST)" ]; then \
		echo "Setting default database configuration..."; \
		export DB_HOST=localhost; \
		export DB_PORT=5432; \
		export DB_USER=postgres; \
		export DB_PASSWORD=postgres; \
		export DB_NAME=yamata_db; \
	fi
	@if ! command -v createdb >/dev/null 2>&1; then \
		echo "Error: PostgreSQL client (createdb) not found. Please install postgresql-client"; \
		exit 1; \
	fi
	@if ! command -v psql >/dev/null 2>&1; then \
		echo "Error: PostgreSQL client (psql) not found. Please install postgresql-client"; \
		exit 1; \
	fi
	@if ! pg_isready -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) >/dev/null 2>&1; then \
		echo "Error: Cannot connect to PostgreSQL at $(DB_HOST):$(DB_PORT)"; \
		echo "Please ensure PostgreSQL is running"; \
		exit 1; \
	fi
	@echo "Creating database $(DB_NAME)..."
	@createdb -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) $(DB_NAME) 2>/dev/null || echo "Database might already exist"
	@echo "Running migrations..."
	@psql -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) -d $(DB_NAME) -f migrations/run_all_up.sql
	@echo "Database creation and migrations completed successfully"
