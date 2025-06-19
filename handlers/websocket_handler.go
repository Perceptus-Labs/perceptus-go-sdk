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
	EnableCompression: true,
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
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
	zap.L().Info("WebSocket upgrade request received",
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()))

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.L().Error("Failed to upgrade to websocket", zap.Error(err))
		return
	}
	defer conn.Close()

	zap.L().Info("WebSocket connection upgraded successfully")

	// Create new robot session
	sessionID := uuid.New().String()
	session := NewRoboSession(sessionID, conn, redisClient)
	session.Logger.Info("New robot session started")

	// Initialize handlers
	intentionHandler := InitIntentionHandler(session)
	session.IntentionHandler = intentionHandler

	audioHandler, err := InitAudioHandler(session)
	if err != nil {
		session.Logger.Error("Failed to initialize audio handler", zap.Error(err))
		session.Stop()
		return
	}

	videoHandler := InitVideoHandler(session)

	session.VideoHandler = videoHandler
	session.AudioHandler = audioHandler

	// Send welcome message immediately after upgrade (before starting message listener)
	welcomeMsg := WebSocketMessage{
		Type: "welcome",
		Data: map[string]interface{}{
			"session_id": session.ID,
			"message":    "Robot session started successfully",
			"timestamp":  time.Now(),
		},
		Timestamp: time.Now(),
	}
	if err := conn.WriteJSON(welcomeMsg); err != nil {
		session.Logger.Error("Failed to send welcome message", zap.Error(err))
	} else {
		session.Logger.Info("Welcome message sent successfully")
	}

	// Start the main session orchestrator goroutine
	go session.handleSessionOrchestrator(audioHandler, videoHandler, intentionHandler)

	// Handle incoming websocket messages
	go session.listenWebsocketMessages(conn, audioHandler)

	// The session will keep running until the WebSocket connection is closed
	// or a stop command is received
}

func (session *RoboSession) listenWebsocketMessages(conn *websocket.Conn, audioHandler *AudioHandler) {
	session.Logger.Info("Starting WebSocket message listener")

	// Handle incoming websocket messages
	for {
		// First try to read as JSON message
		var msg WebSocketMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			// If JSON parsing fails, try to read as raw message
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				session.Logger.Error("WebSocket connection closed unexpectedly", zap.Error(err))
			} else if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				session.Logger.Info("WebSocket connection closed normally")
			} else {
				session.Logger.Warn("Failed to read JSON message, trying raw message", zap.Error(err))

				// Try to read as raw message
				messageType, message, readErr := conn.ReadMessage()
				if readErr != nil {
					if websocket.IsUnexpectedCloseError(readErr, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						session.Logger.Error("WebSocket error reading raw message", zap.Error(readErr))
					}
					break
				}

				// Handle raw message
				session.Logger.Debug("Received raw message", zap.Int("type", messageType), zap.String("message", string(message)))

				// If it's a text message, try to parse as JSON
				if messageType == websocket.TextMessage {
					if jsonErr := json.Unmarshal(message, &msg); jsonErr == nil {
						// Successfully parsed as JSON, continue with normal handling
					} else {
						// Treat as raw text message
						session.Logger.Debug("Received text message", zap.String("content", string(message)))
						continue
					}
				} else if messageType == websocket.BinaryMessage {
					// Handle binary message (likely audio data)
					session.Logger.Debug("Received binary message", zap.Int("size", len(message)))
					if err := audioHandler.ProcessAudioData(message); err != nil {
						session.Logger.Error("Failed to process binary audio data", zap.Error(err))
					}
					continue
				}
			}
			break
		}

		session.Logger.Debug("Received WebSocket message", zap.String("type", msg.Type))

		// Handle different message types
		switch msg.Type {
		case "config":
			session.handleConfigMessage(msg.Data)
		case "audio_data":
			session.handleAudioData(audioHandler, msg.Data)
		case "ping":
			// Send pong response
			pongMsg := WebSocketMessage{
				Type:      "pong",
				Timestamp: time.Now(),
			}
			if err := conn.WriteJSON(pongMsg); err != nil {
				session.Logger.Error("Failed to send pong", zap.Error(err))
			}
		case "stop":
			session.Logger.Info("Received stop command from client")

			// Send SESSION_END to all channels to stop all goroutines
			session.SendToAllChannels(models.SESSION_END)

			// Stop the session
			session.Stop()

			// Send confirmation back to client
			stopMsg := WebSocketMessage{
				Type: "stop_confirmation",
				Data: map[string]interface{}{
					"session_id": session.ID,
					"message":    "Session stopped successfully",
				},
				Timestamp: time.Now(),
			}
			if err := conn.WriteJSON(stopMsg); err != nil {
				session.Logger.Error("Failed to send stop confirmation", zap.Error(err))
			}

			return
		default:
			session.Logger.Warn("Unknown message type", zap.String("type", msg.Type))
		}
	}

	// Connection closed, stop the session
	session.Logger.Info("WebSocket connection closed, stopping session")
	session.SendToAllChannels(models.SESSION_END)
	session.Stop()
}

func (session *RoboSession) handleSessionOrchestrator(audioHandler *AudioHandler, videoHandler *VideoHandler, intentionHandler *IntentionHandler) {
	session.Logger.Info("Session orchestrator started")

	// Start the main event loop
	for session.IsActive {
		time.Sleep(30 * time.Second)
		// Periodic heartbeat
		session.Logger.Debug("Session heartbeat")
		// session.sendWebSocketMessage("heartbeat", map[string]interface{}{
		// 	"session_id": session.ID,
		// 	"uptime":     time.Since(session.StartTime).String(),
		// })
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

	// session.sendWebSocketMessage("config_updated", map[string]interface{}{
	// 	"video_frequency": session.VideoFrequency.String(),
	// 	"audio_frequency": session.AudioFrequency.String(),
	// })
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

// func (session *RoboSession) sendWebSocketMessage(msgType string, data interface{}) {
// 	msg := WebSocketMessage{
// 		Type:      msgType,
// 		Data:      data,
// 		Timestamp: time.Now(),
// 	}

// 	err := session.Connection.WriteJSON(msg)
// 	if err != nil {
// 		session.Logger.Error("Failed to send websocket message", zap.Error(err), zap.String("type", msgType))
// 	}
// }

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
		// session.sendWebSocketMessage("orchestrator_triggered", map[string]interface{}{
		// 	"session_id": session.ID,
		// 	"intention":  intention.Description,
		// 	"timestamp":  time.Now(),
		// })
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
