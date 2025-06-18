package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/handlers"
	"github.com/lpernett/godotenv"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Load environment variables from .env file
// Without this, it tries to use the SSL cert logic
func init() {
	// Initialize zap logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	zap.ReplaceGlobals(logger)

	zap.L().Info("Loading environment variables")
	err = godotenv.Load()
	if err != nil {
		zap.L().Warn("Error loading .env file")
	}
}

func main() {
	// Set up logging
	zap.L().Info("Server Version: Perceptus Robot SDK V1")

	// Set up Redis connection
	redisClient := redis.NewClient(&redis.Options{
		Addr:        os.Getenv("REDIS_HOST"),
		Password:    os.Getenv("REDIS_PASSWORD"),
		DB:          0,
		DialTimeout: 20 * time.Second, // initial connection timeout
	})

	redisCtx, cancelRedis := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRedis()

	_, err := redisClient.Ping(redisCtx).Result()
	if err != nil {
		zap.L().Fatal("Failed to connect to Redis", zap.Error(err))
	}
	zap.L().Info("Successfully connected to Redis")

	// Main websocket endpoint for robot sessions
	http.HandleFunc("/robot-session", func(w http.ResponseWriter, r *http.Request) {
		handlers.HandleRobotSession(w, r, redisClient)
	})

	// Set up signal handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Create a context with a timeout for the server
	_, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverExit := make(chan struct{})

	// Start HTTP server in a goroutine
	go func() {
		port := ":" + os.Getenv("PORT")
		if port == ":" {
			port = ":8080"
		}
		zap.L().Info("Starting server", zap.String("port", port))
		zap.L().Fatal("Server error", zap.Error(http.ListenAndServe(port, nil)))
		close(serverExit)
	}()

	// On termination, close all connections and shut down the server
	select {
	case <-stop:
		zap.L().Info("Shutting down server...")
	case <-serverExit:
		zap.L().Info("Server exited unexpectedly...")
	}

	// Cancel the context to stop the connection reset scheduler
	cancelServer()

	zap.L().Info("Server shut down gracefully")
}
