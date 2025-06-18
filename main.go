package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lpernett/godotenv"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

// Load environment variables from .env file
// Without this, it tries to use the SSL cert logic
func init() {
	log.Info("Loading environment variables")
	err := godotenv.Load()
	if err != nil {
		log.Warn("Error loading .env file")
	}
}

func main() {
	// Set up logging
	log.SetLevel(log.DebugLevel)
	log.SetFormatter(&log.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})
	log.Info("Server Version: Agent Editor V2")

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
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Info("Successfully connected to Redis")

	// Define HTTP routes
	// http.HandleFunc("/healthz", handlers.HealthCheckHandler)
	// http.HandleFunc("/twilio-va", func(w http.ResponseWriter, r *http.Request) {
	// 	handlers.TwilioSocketHandler(w, r, redisClient, influxService)
	// })
	// http.HandleFunc("/telnyx-va/texml", func(w http.ResponseWriter, r *http.Request) {
	// 	handlers.TelnyxSocketHandler(w, r, redisClient, influxService)
	// })
	// http.HandleFunc("/telnyx-va/voice-api-v2", func(w http.ResponseWriter, r *http.Request) {
	// 	handlers.TelnyxSocketHandler(w, r, redisClient, influxService)
	// })
	// http.HandleFunc("/text", func(w http.ResponseWriter, r *http.Request) {
	// 	handlers.TextChatEndpointHandler(w, r, redisClient, influxService)
	// })

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
		log.Info("Starting server on...", port)
		log.Fatal(http.ListenAndServe(port, nil))
		close(serverExit)
	}()

	// On termination, close all connections and shut down the server
	select {
	case <-stop:
		log.Info("Shutting down server...")
	case <-serverExit:
		log.Info("Server exited unexpectedly...")
	}

	// Cancel the context to stop the connection reset scheduler
	cancelServer()

	log.Info("Server shut down gracefully")
}
