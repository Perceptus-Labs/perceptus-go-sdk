// handlers/websocket_handler.go

package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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

		VideoFrequency: 30 * time.Second,       // Default: take picture every 30 seconds
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
	if rs.IsActive {
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

func (rs *RoboSession) setupHandlers() {
	intentionHandler := InitIntentionHandler(rs)
	rs.IntentionHandler = intentionHandler

	audioHandler, err := InitAudioHandler(rs)
	if err != nil {
		rs.Logger.Error("Failed to initialize audio handler", zap.Error(err))
		rs.Stop()
		return
	}
	rs.AudioHandler = audioHandler

	videoHandler := InitVideoHandler(rs)
	rs.VideoHandler = videoHandler
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

	zap.L().Info("WebSocket connection upgraded successfully")

	// Create new robot session
	sessionID := uuid.New().String()
	session := NewRoboSession(sessionID, conn, redisClient)
	session.Logger.Info("New robot session started")

	// Setup handlers
	session.setupHandlers()

	// Send welcome message immediately after upgrade (before starting message listener)
	welcomeMsg := WebSocketMessage{
		Type: "text",
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

	// Handle incoming websocket messages
	go session.listenWebsocketMessages(conn)
}

func (rs *RoboSession) listenWebsocketMessages(conn *websocket.Conn) {
	rs.Logger.Info("Starting WebSocket message listener")

	// Handle incoming websocket messages
	for {
		// First try to read as JSON message
		var msg WebSocketMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			rs.Logger.Error("Failed to read JSON message", zap.Error(err))
			break
		}

		rs.Logger.Debug("Received WebSocket message", zap.String("type", msg.Type))

		// Handle different message types
		switch msg.Type {
		case "config":
			rs.handleConfigMessage(msg.Data)
		case "audio_data":
			rs.handleAudioData(rs.AudioHandler, msg.Data)
		case "video_data":
			rs.handleVideoData(msg)
		case "ping":
			// Send pong response
			pongMsg := WebSocketMessage{
				Type:      "pong",
				Timestamp: time.Now(),
			}
			if err := conn.WriteJSON(pongMsg); err != nil {
				rs.Logger.Error("Failed to send pong", zap.Error(err))
			}
		case "stop":
			rs.Logger.Info("Received stop command from client")

			// Send SESSION_END to all channels to stop all goroutines
			rs.SendToAllChannels(models.SESSION_END)

			// Stop the session
			rs.Stop()

			// Send confirmation back to client
			stopMsg := WebSocketMessage{
				Type: "text",
				Data: map[string]interface{}{
					"session_id": rs.ID,
					"message":    "Session stopped successfully",
				},
				Timestamp: time.Now(),
			}
			if err := conn.WriteJSON(stopMsg); err != nil {
				rs.Logger.Error("Failed to send stop confirmation", zap.Error(err))
			}

			return
		default:
			rs.Logger.Warn("Unknown message type", zap.String("type", msg.Type))
		}
	}

	// Connection closed, stop the session
	rs.Logger.Info("WebSocket connection closed, stopping session")
	rs.SendToAllChannels(models.SESSION_END)
	rs.Stop()
}

func (rs *RoboSession) handleConfigMessage(data interface{}) {
	configData, ok := data.(map[string]interface{})
	if !ok {
		rs.Logger.Error("Invalid config data format")
		return
	}

	// Parse video frequency
	if videoFreq, exists := configData["video_frequency"]; exists {
		if freqStr, ok := videoFreq.(string); ok {
			if duration, err := time.ParseDuration(freqStr); err == nil {
				rs.VideoFrequency = duration
				rs.Logger.Info("Updated video frequency", zap.Duration("frequency", duration))
			}
		}
	}

	// Parse audio frequency
	if audioFreq, exists := configData["audio_frequency"]; exists {
		if freqStr, ok := audioFreq.(string); ok {
			if duration, err := time.ParseDuration(freqStr); err == nil {
				rs.AudioFrequency = duration
				rs.Logger.Info("Updated audio frequency", zap.Duration("frequency", duration))
			}
		}
	}

	rs.sendWebSocketMessage("config_updated", map[string]interface{}{
		"video_frequency": rs.VideoFrequency.String(),
		"audio_frequency": rs.AudioFrequency.String(),
	})
}

func (rs *RoboSession) handleAudioData(audioHandler *AudioHandler, data interface{}) {
	// Handle audio data similar to Twilio media events
	rs.Logger.Debug("Received audio data")

	audioBytes, err := rs.extractAudioBytes(data)
	if err != nil {
		rs.Logger.Warn("Unable to extract audio bytes", zap.Error(err))
		return
	}

	// Hand off to the audio handler
	if err := audioHandler.ProcessAudioData(audioBytes); err != nil {
		rs.Logger.Error("Failed to process audio data", zap.Error(err))
	}
}

func (rs *RoboSession) extractAudioBytes(data interface{}) ([]byte, error) {
	switch v := data.(type) {
	case []byte:
		return v, nil

	case string:
		// assume base64-encoded audio
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("base64 decode string: %w", err)
		}
		return decoded, nil

	default:
		return nil, fmt.Errorf("unsupported data type %T", data)
	}
}

func (rs *RoboSession) sendWebSocketMessage(msgType string, data interface{}) {
	msg := WebSocketMessage{
		Type:      msgType,
		Data:      data,
		Timestamp: time.Now(),
	}
	if err := rs.Connection.WriteJSON(msg); err != nil {
		rs.Logger.Error("failed to send ws message",
			zap.String("type", msgType), zap.Error(err))
	}
}

func triggerOrchestrator(rs *RoboSession, intention models.IntentionResult) {
	rs.Logger.Info("Triggering orchestrator", zap.Any("intention", intention))
	orchestratorEndpoint := os.Getenv("ORCHESTRATOR_ENDPOINT")
	apiKey := os.Getenv("ORCHESTRATOR_API_KEY")

	if orchestratorEndpoint == "" || apiKey == "" {
		rs.Logger.Error("Orchestrator endpoint or API key not configured")
		return
	}

	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Prepare the payload
	payload := map[string]interface{}{
		"session_id":          rs.ID,
		"intention":           intention,
		"environment_context": intention.EnvironmentContext,
		"timestamp":           time.Now(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rs.Logger.Error("Failed to marshal orchestrator payload", zap.Error(err))
		return
	}

	// Make the API call
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", orchestratorEndpoint,
		bytes.NewBuffer(payloadBytes))
	if err != nil {
		rs.Logger.Error("Failed to create orchestrator request", zap.Error(err))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		rs.Logger.Error("Failed to call orchestrator", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		rs.Logger.Info("Successfully triggered orchestrator")
		rs.LastActionTime = time.Now()

		// Notify client of successful orchestrator trigger
		// session.sendWebSocketMessage("orchestrator_triggered", map[string]interface{}{
		// 	"session_id": session.ID,
		// 	"intention":  intention.Description,
		// 	"timestamp":  time.Now(),
		// })
	} else {
		rs.Logger.Error("Orchestrator returned error status", zap.Int("status", resp.StatusCode))
	}
}

// handles API requests to capture an image
func (rs *RoboSession) handleVideoData(msg WebSocketMessage) {
	b64, ok := msg.Data.(string)
	if !ok {
		rs.Logger.Warn("video_data payload not a string", zap.Any("data", msg.Data))
		return
	}

	if !strings.HasPrefix(b64, "data:image") {
		b64 = "data:image/jpeg;base64," + b64
	}
	// 1) echo back so the <img id="videoPreview"> renders it
	rs.sendWebSocketMessage("video_frame", map[string]string{
		"image_b64": b64,
	})

	// 2) then hand off for analysis
	select {
	case rs.VideoAnalysisCh <- b64:
	default:
		rs.Logger.Warn("video_analysis channel full, dropping frame")
	}
}
