package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow connections from any origin
	},
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
		log.WithError(err).Error("Failed to upgrade to websocket")
		return
	}
	defer conn.Close()

	// Create new robot session
	sessionID := uuid.New().String()
	session := models.NewRoboSession(sessionID, conn, redisClient)
	session.Logger.Info("New robot session started")

	// Start the session orchestrator
	go HandleStartSession(session)

	// Handle incoming websocket messages
	for {
		var msg WebSocketMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				session.Logger.WithError(err).Error("WebSocket error")
			}
			break
		}

		// Handle different message types
		switch msg.Type {
		case "config":
			handleConfigMessage(session, msg.Data)
		case "audio_data":
			handleAudioData(session, msg.Data)
		case "ping":
			sendWebSocketMessage(session, "pong", nil)
		default:
			session.Logger.Warn("Unknown message type:", msg.Type)
		}
	}

	// Clean up session
	session.Logger.Info("Robot session ended")
	session.Close()
}

func HandleStartSession(session *models.RoboSession) {
	session.Logger.Info("Starting robot session orchestrator")

	// Initialize handlers
	audioHandler, err := InitAudioHandler(session)
	if err != nil {
		session.Logger.WithError(err).Error("Failed to initialize audio handler")
		return
	}

	videoHandler := InitVideoHandler(session)
	intentionHandler := InitIntentionHandler(session)

	// Link video handler to intention handler
	intentionHandler.SetVideoHandler(videoHandler)

	// Start the periodic video capture
	go videoHandler.StartPeriodicCapture()

	// Start the intention processor
	go intentionHandler.StartIntentionProcessor()

	// Start the main event loop
	for session.IsActive {
		select {
		case <-session.CurrentContext.Done():
			session.Logger.Info("Session context cancelled")
			return

		case transcript := <-session.TranscriptionCh:
			session.Logger.Debug("Received transcript:", transcript)

			if transcript == "<END_OF_SPEECH>" {
				// Process the accumulated transcript for intention
				if session.CurrentTranscript != "" {
					session.Logger.Info("Processing transcript for intention:", session.CurrentTranscript)
					go intentionHandler.ProcessTranscriptForIntention(session.CurrentTranscript)

					// Reset transcript buffer
					session.CurrentTranscript = ""
				}
			} else {
				// Accumulate transcript
				session.CurrentTranscript += transcript + " "
			}

		case intentionResult := <-session.IntentionCh:
			session.Logger.Info("Received intention result:", intentionResult.Description)

			if intentionResult.HasClearIntention {
				session.Logger.Info("Clear intention detected, triggering orchestrator")
				go triggerOrchestrator(session, intentionResult)
			}

			// Send intention result to client
			sendWebSocketMessage(session, "intention_result", intentionResult)

		case videoAnalysis := <-session.VideoAnalysisCh:
			session.Logger.Debug("Received video analysis")
			// Store environment context and send to client
			sendWebSocketMessage(session, "video_analysis", videoAnalysis)

		case <-time.After(30 * time.Second):
			// Periodic heartbeat
			session.Logger.Debug("Session heartbeat")
			sendWebSocketMessage(session, "heartbeat", map[string]interface{}{
				"session_id": session.ID,
				"uptime":     time.Since(session.StartTime).String(),
			})
		}
	}

	// Cleanup
	audioHandler.Close()
	videoHandler.Close()
	intentionHandler.Close()
}

func handleConfigMessage(session *models.RoboSession, data interface{}) {
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
				session.Logger.Info("Updated video frequency:", duration)
			}
		}
	}

	// Parse audio frequency
	if audioFreq, exists := configData["audio_frequency"]; exists {
		if freqStr, ok := audioFreq.(string); ok {
			if duration, err := time.ParseDuration(freqStr); err == nil {
				session.AudioFrequency = duration
				session.Logger.Info("Updated audio frequency:", duration)
			}
		}
	}

	sendWebSocketMessage(session, "config_updated", map[string]interface{}{
		"video_frequency": session.VideoFrequency.String(),
		"audio_frequency": session.AudioFrequency.String(),
	})
}

func handleAudioData(session *models.RoboSession, data interface{}) {
	// This would handle raw audio data from the client
	// For now, we'll just acknowledge receipt
	sendWebSocketMessage(session, "audio_received", map[string]interface{}{
		"timestamp": time.Now(),
	})
}

func sendWebSocketMessage(session *models.RoboSession, msgType string, data interface{}) {
	msg := WebSocketMessage{
		Type:      msgType,
		Data:      data,
		Timestamp: time.Now(),
	}

	err := session.Connection.WriteJSON(msg)
	if err != nil {
		session.Logger.WithError(err).Error("Failed to send websocket message")
	}
}

func triggerOrchestrator(session *models.RoboSession, intention models.IntentionResult) {
	orchestratorEndpoint := getOrchestratorEndpoint()
	apiKey := getOrchestratorAPIKey()

	if orchestratorEndpoint == "" || apiKey == "" {
		session.Logger.Error("Orchestrator endpoint or API key not configured")
		return
	}

	// Prepare the payload
	payload := map[string]interface{}{
		"session_id":          session.ID,
		"intention":           intention,
		"environment_context": intention.EnvironmentContext,
		"timestamp":           time.Now(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		session.Logger.WithError(err).Error("Failed to marshal orchestrator payload")
		return
	}

	// Make the API call
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(session.CurrentContext, "POST", orchestratorEndpoint,
		bytes.NewBuffer(payloadBytes))
	if err != nil {
		session.Logger.WithError(err).Error("Failed to create orchestrator request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		session.Logger.WithError(err).Error("Failed to call orchestrator")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		session.Logger.Info("Successfully triggered orchestrator")
		session.LastActionTime = time.Now()

		// Notify client of successful orchestrator trigger
		sendWebSocketMessage(session, "orchestrator_triggered", map[string]interface{}{
			"session_id": session.ID,
			"intention":  intention.Description,
			"timestamp":  time.Now(),
		})
	} else {
		session.Logger.Error("Orchestrator returned error status:", resp.StatusCode)
	}
}

func getOrchestratorEndpoint() string {
	endpoint := os.Getenv("ORCHESTRATOR_ENDPOINT")
	if endpoint == "" {
		return "http://localhost:8080" // Default fallback
	}
	return endpoint
}

func getOrchestratorAPIKey() string {
	return os.Getenv("ORCHESTRATOR_API_KEY")
}
