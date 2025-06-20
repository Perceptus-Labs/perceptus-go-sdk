#!/bin/bash

# Perceptus Go SDK Deployment Script
# This script helps deploy the Perceptus Go SDK using Docker

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
IMAGE_NAME="perceptus-go-sdk"
TAG=${1:-latest}
CONTAINER_NAME="perceptus-go-sdk"
PORT=${2:-8080}

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if Docker is running
check_docker() {
    if ! docker info > /dev/null 2>&1; then
        print_error "Docker is not running. Please start Docker and try again."
        exit 1
    fi
    print_success "Docker is running"
}

# Function to check if .env file exists
check_env() {
    if [ ! -f .env ]; then
        print_warning ".env file not found. Creating from example..."
        if [ -f config.example.env ]; then
            cp config.example.env .env
            print_warning "Please edit .env file with your configuration before running the application."
        else
            print_error "No .env file or config.example.env found. Please create a .env file with required environment variables."
            exit 1
        fi
    fi
}

# Function to build the Docker image
build_image() {
    print_status "Building Docker image..."
    docker build -t ${IMAGE_NAME}:${TAG} .
    print_success "Docker image built successfully"
}

# Function to stop and remove existing container
cleanup_container() {
    if docker ps -a --format "table {{.Names}}" | grep -q "^${CONTAINER_NAME}$"; then
        print_status "Stopping existing container..."
        docker stop ${CONTAINER_NAME} || true
        docker rm ${CONTAINER_NAME} || true
        print_success "Existing container cleaned up"
    fi
}

# Function to run the container
run_container() {
    print_status "Starting container..."
    
    # Check if .env file exists and source it
    if [ -f .env ]; then
        export $(cat .env | grep -v '^#' | xargs)
    fi
    
    docker run -d \
        --name ${CONTAINER_NAME} \
        -p ${PORT}:8080 \
        --env-file .env \
        --restart unless-stopped \
        ${IMAGE_NAME}:${TAG}
    
    print_success "Container started successfully"
}

# Function to check container health
check_health() {
    print_status "Checking container health..."
    sleep 5
    
    if docker ps --format "table {{.Names}}" | grep -q "^${CONTAINER_NAME}$"; then
        print_success "Container is running"
        
        # Check if the application is responding
        if curl -f http://localhost:${PORT}/health > /dev/null 2>&1; then
            print_success "Application is healthy and responding"
        else
            print_warning "Application is running but health check failed. Check logs with: docker logs ${CONTAINER_NAME}"
        fi
    else
        print_error "Container failed to start. Check logs with: docker logs ${CONTAINER_NAME}"
        exit 1
    fi
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [TAG] [PORT]"
    echo ""
    echo "Arguments:"
    echo "  TAG     Docker image tag (default: latest)"
    echo "  PORT    Port to expose (default: 8080)"
    echo ""
    echo "Examples:"
    echo "  $0                    # Deploy with latest tag on port 8080"
    echo "  $0 v1.0.0            # Deploy with v1.0.0 tag on port 8080"
    echo "  $0 latest 9090       # Deploy with latest tag on port 9090"
}

# Function to show logs
show_logs() {
    print_status "Showing container logs..."
    docker logs -f ${CONTAINER_NAME}
}

# Function to stop the application
stop_app() {
    print_status "Stopping application..."
    docker stop ${CONTAINER_NAME} || true
    docker rm ${CONTAINER_NAME} || true
    print_success "Application stopped"
}

# Main deployment function
deploy() {
    print_status "Starting deployment..."
    
    check_docker
    check_env
    build_image
    cleanup_container
    run_container
    check_health
    
    print_success "Deployment completed successfully!"
    print_status "Application is available at: http://localhost:${PORT}"
    print_status "Example client is available at: http://localhost:${PORT}/example_client.html"
    print_status "View logs with: docker logs -f ${CONTAINER_NAME}"
}

# Handle command line arguments
case "${1:-deploy}" in
    "deploy")
        deploy
        ;;
    "logs")
        show_logs
        ;;
    "stop")
        stop_app
        ;;
    "restart")
        stop_app
        deploy
        ;;
    "help"|"-h"|"--help")
        show_usage
        ;;
    *)
        deploy
        ;;
esac 