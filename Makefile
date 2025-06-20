# Perceptus Go SDK Makefile
# Common commands for development and deployment

.PHONY: help build run test clean docker-build docker-run docker-stop docker-logs deploy

# Default target
help:
	@echo "Perceptus Go SDK - Available Commands:"
	@echo ""
	@echo "Development:"
	@echo "  make build        - Build the Go application"
	@echo "  make run          - Run the application locally"
	@echo "  make test         - Run tests"
	@echo "  make clean        - Clean build artifacts"
	@echo ""
	@echo "Docker:"
	@echo "  make docker-build - Build Docker image"
	@echo "  make docker-run   - Run with Docker Compose"
	@echo "  make docker-stop  - Stop Docker containers"
	@echo "  make docker-logs  - Show Docker logs"
	@echo ""
	@echo "Deployment:"
	@echo "  make deploy       - Deploy using deploy.sh script"
	@echo "  make deploy-stop  - Stop deployed application"
	@echo ""
	@echo "Utilities:"
	@echo "  make fmt          - Format Go code"
	@echo "  make lint         - Lint Go code"
	@echo "  make deps         - Download dependencies"

# Development commands
build:
	@echo "Building Perceptus Go SDK..."
	go build -o perceptus-go-sdk .

run:
	@echo "Running Perceptus Go SDK..."
	./perceptus-go-sdk

test:
	@echo "Running tests..."
	go test -v ./...

clean:
	@echo "Cleaning build artifacts..."
	rm -f perceptus-go-sdk
	go clean

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t perceptus-go-sdk:latest .

docker-run:
	@echo "Starting services with Docker Compose..."
	docker-compose up -d

docker-stop:
	@echo "Stopping Docker containers..."
	docker-compose down

docker-logs:
	@echo "Showing Docker logs..."
	docker-compose logs -f

# Deployment commands
deploy:
	@echo "Deploying application..."
	./deploy.sh

deploy-stop:
	@echo "Stopping deployed application..."
	./deploy.sh stop

# Utility commands
fmt:
	@echo "Formatting Go code..."
	go fmt ./...

lint:
	@echo "Linting Go code..."
	golangci-lint run

deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Health check
health:
	@echo "Checking application health..."
	@curl -f http://localhost:8080/health || echo "Health check failed"

# Development setup
setup:
	@echo "Setting up development environment..."
	@if [ ! -f .env ]; then \
		if [ -f config.example.env ]; then \
			cp config.example.env .env; \
			echo "Created .env from config.example.env"; \
		else \
			echo "Please create a .env file with required environment variables"; \
		fi; \
	fi
	@make deps
	@echo "Development environment setup complete!" 