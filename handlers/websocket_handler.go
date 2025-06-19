package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type RoboSession struct {
	ID                   string
	CurrentContext       context.Context
	CancelCurrentContext context.CancelFunc
	Connection           *websocket.Conn
	RedisClient          *redis.Client
	Logger               *zap.Logger

	// Channels for communication between handlers
	TranscriptionCh chan string
	InterruptionCh  chan string
	VideoAnalysisCh chan string
	IntentionCh     chan models.IntentionResult

	// Session state
	IsActive     bool
	StartTime    time.Time
	LastActivity time.Time

	// Configuration
	VideoFrequency time.Duration // How often to take pictures
	AudioFrequency time.Duration // How often to process audio (if needed)

	// Current transcript buffer
	CurrentTranscript string
	LastActionTime    time.Time

	VideoHandler     *VideoHandler
	AudioHandler     *AudioHandler
	IntentionHandler *IntentionHandler
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow connections from any origin
	},
}

func NewRoboSession(id string, conn *websocket.Conn, redisClient *redis.Client) *RoboSession {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a logger with session ID context
	logger := zap.L().With(zap.String("session_id", id))

	session := &RoboSession{
		ID:                   id,
		CurrentContext:       ctx,
		CancelCurrentContext: cancel,
		Connection:           conn,
		RedisClient:          redisClient,
		Logger:               logger,

		TranscriptionCh: make(chan string, 100),
		InterruptionCh:  make(chan string, 100),
		VideoAnalysisCh: make(chan string, 100),
		IntentionCh:     make(chan models.IntentionResult, 10),

		IsActive:     true,
		StartTime:    time.Now(),
		LastActivity: time.Now(),

		VideoFrequency: 10 * time.Second,       // Default: take picture every 10 seconds
		AudioFrequency: 100 * time.Millisecond, // Default: process audio continuously

		CurrentTranscript: "",
		LastActionTime:    time.Now(),
	}

	return session
}

func (rs *RoboSession) UpdateContext() {
	rs.CancelCurrentContext()
	rs.CurrentContext, rs.CancelCurrentContext = context.WithCancel(context.Background())
	rs.LastActivity = time.Now()
}

func (rs *RoboSession) Stop() {
	rs.Logger.Info("Stopping session")
	rs.IsActive = false

	// Send SESSION_END to all channels to stop all goroutines
	rs.SendToAllChannels(models.SESSION_END)

	// Cancel current context
	rs.CancelCurrentContext()

	// Close all channels
	close(rs.TranscriptionCh)
	close(rs.InterruptionCh)
	close(rs.VideoAnalysisCh)
	close(rs.IntentionCh)

	if rs.Connection != nil {
		rs.Connection.Close()
	}
}

func (rs *RoboSession) SendToAllChannels(message string) {
	// Send to all channels that accept strings
	select {
	case rs.TranscriptionCh <- message:
	default:
	}
	select {
	case rs.InterruptionCh <- message:
	default:
	}
	select {
	case rs.VideoAnalysisCh <- message:
	default:
	}
}

func (rs *RoboSession) Close() {
	rs.Stop()
}

type SessionConfig struct {
	VideoFrequency time.Duration `json:"video_frequency"`
	AudioFrequency time.Duration `json:"audio_frequency"`
}

type WebSocketMessage struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

func HandleRobotSession(w http.ResponseWriter, r *http.Request, redisClient *redis.Client) {
	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.L().Error("Failed to upgrade to websocket", zap.Error(err))
		return
	}
	defer conn.Close()

	// Create new robot session
	sessionID := uuid.New().String()
	session := NewRoboSession(sessionID, conn, redisClient)
	session.Logger.Info("New robot session started")

	intentionHandler := InitIntentionHandler(session)
	session.IntentionHandler = intentionHandler
	// Initialize handlers (they start their own goroutines)
	audioHandler, err := InitAudioHandler(session)
	if err != nil {
		session.Logger.Error("Failed to initialize audio handler", zap.Error(err))
		session.Stop()
		return
	}

	videoHandler := InitVideoHandler(session)

	session.VideoHandler = videoHandler
	session.AudioHandler = audioHandler

	// Start the main session orchestrator goroutine
	go session.handleSessionOrchestrator(audioHandler, videoHandler, intentionHandler)

	// Handle incoming websocket messages
	go session.listenWebsocketMessages(conn, audioHandler)

	// Clean up session
	session.Logger.Info("Robot session ended")
	session.Stop()
}

func (session *RoboSession) listenWebsocketMessages(conn *websocket.Conn, audioHandler *AudioHandler) {
	// Handle incoming websocket messages
	for {
		var msg WebSocketMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				session.Logger.Error("WebSocket error", zap.Error(err))
			}
			break
		}

		// Handle different message types
		switch msg.Type {
		case "config":
			session.handleConfigMessage(msg.Data)
		case "audio_data":
			session.handleAudioData(audioHandler, msg.Data)
		case "ping":
			session.sendWebSocketMessage("pong", nil)
		case "stop":
			session.Logger.Info("Received stop command from client")

			// Send SESSION_END to all channels to stop all goroutines
			session.SendToAllChannels(models.SESSION_END)

			// Stop the session
			session.Stop()

			// Send confirmation back to client
			session.sendWebSocketMessage("stop_confirmation", map[string]interface{}{
				"session_id": session.ID,
				"message":    "Session stopped successfully",
			})

			return
		default:
			session.Logger.Warn("Unknown message type", zap.String("type", msg.Type))
		}
	}
}

func (session *RoboSession) handleSessionOrchestrator(audioHandler *AudioHandler, videoHandler *VideoHandler, intentionHandler *IntentionHandler) {
	session.Logger.Info("Session orchestrator started")

	// Start the main event loop
	for session.IsActive {
		time.Sleep(30 * time.Second)
		// Periodic heartbeat
		session.Logger.Debug("Session heartbeat")
		session.sendWebSocketMessage("heartbeat", map[string]interface{}{
			"session_id": session.ID,
			"uptime":     time.Since(session.StartTime).String(),
		})
	}

	// Cleanup handlers
	session.Logger.Info("Cleaning up handlers")
	audioHandler.Close()
	videoHandler.Close()
	intentionHandler.Close()
}

func (session *RoboSession) handleConfigMessage(data interface{}) {
	configData, ok := data.(map[string]interface{})
	if !ok {
		session.Logger.Error("Invalid config data format")
		return
	}

	// Parse video frequency
	if videoFreq, exists := configData["video_frequency"]; exists {
		if freqStr, ok := videoFreq.(string); ok {
			if duration, err := time.ParseDuration(freqStr); err == nil {
				session.VideoFrequency = duration
				session.Logger.Info("Updated video frequency", zap.Duration("frequency", duration))
			}
		}
	}

	// Parse audio frequency
	if audioFreq, exists := configData["audio_frequency"]; exists {
		if freqStr, ok := audioFreq.(string); ok {
			if duration, err := time.ParseDuration(freqStr); err == nil {
				session.AudioFrequency = duration
				session.Logger.Info("Updated audio frequency", zap.Duration("frequency", duration))
			}
		}
	}

	session.sendWebSocketMessage("config_updated", map[string]interface{}{
		"video_frequency": session.VideoFrequency.String(),
		"audio_frequency": session.AudioFrequency.String(),
	})
}

func (session *RoboSession) handleAudioData(audioHandler *AudioHandler, data interface{}) {
	// Handle audio data similar to Twilio media events
	session.Logger.Debug("Received audio data")

	// Extract audio data from the message
	// Assuming the data comes as base64 encoded audio or raw bytes
	var audioBytes []byte

	switch v := data.(type) {
	case string:
		// If it's a base64 encoded string, decode it
		// This would need proper base64 decoding in real implementation
		session.Logger.Debug("Received audio data as string", zap.Int("length", len(v)))
		audioBytes = []byte(v) // Simplified - in reality you'd base64 decode
	case []byte:
		audioBytes = v
	case map[string]interface{}:
		// If it's a structured message like Twilio's media event
		if payload, ok := v["payload"].(string); ok {
			audioBytes = []byte(payload) // Again, would need proper decoding
		}
	default:
		session.Logger.Warn("Unknown audio data format")
		return
	}

	// Send audio data to the audio handler for processing
	if err := audioHandler.ProcessAudioData(audioBytes); err != nil {
		session.Logger.Error("Failed to process audio data", zap.Error(err))
	}
}

func (session *RoboSession) sendWebSocketMessage(msgType string, data interface{}) {
	msg := WebSocketMessage{
		Type:      msgType,
		Data:      data,
		Timestamp: time.Now(),
	}

	err := session.Connection.WriteJSON(msg)
	if err != nil {
		session.Logger.Error("Failed to send websocket message", zap.Error(err), zap.String("type", msgType))
	}
}

func triggerOrchestrator(session *RoboSession, intention models.IntentionResult) {
	session.Logger.Info("Triggering orchestrator", zap.Any("intention", intention))
	orchestratorEndpoint := os.Getenv("ORCHESTRATOR_ENDPOINT")
	apiKey := os.Getenv("ORCHESTRATOR_API_KEY")

	if orchestratorEndpoint == "" || apiKey == "" {
		session.Logger.Error("Orchestrator endpoint or API key not configured")
		return
	}

	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Prepare the payload
	payload := map[string]interface{}{
		"session_id":          session.ID,
		"intention":           intention,
		"environment_context": intention.EnvironmentContext,
		"timestamp":           time.Now(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		session.Logger.Error("Failed to marshal orchestrator payload", zap.Error(err))
		return
	}

	// Make the API call
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", orchestratorEndpoint,
		bytes.NewBuffer(payloadBytes))
	if err != nil {
		session.Logger.Error("Failed to create orchestrator request", zap.Error(err))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		session.Logger.Error("Failed to call orchestrator", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		session.Logger.Info("Successfully triggered orchestrator")
		session.LastActionTime = time.Now()

		// Notify client of successful orchestrator trigger
		session.sendWebSocketMessage("orchestrator_triggered", map[string]interface{}{
			"session_id": session.ID,
			"intention":  intention.Description,
			"timestamp":  time.Now(),
		})
	} else {
		session.Logger.Error("Orchestrator returned error status", zap.Int("status", resp.StatusCode))
	}
}

// HandleCameraCapture handles API requests to capture an image
func HandleCameraCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// This would be used to trigger camera capture for testing or external requests
	zap.L().Info("Camera capture requested via API")

	// In a real implementation, you'd:
	// 1. Get the session ID from the request
	// 2. Find the active session
	// 3. Trigger video capture
	// 4. Return the result

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"status":    "success",
		"message":   "Camera capture triggered",
		"timestamp": time.Now(),
	}

	json.NewEncoder(w).Encode(response)
}
