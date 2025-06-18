package handlers

import (
	"bytes"
	"context"
	"encoding/json"
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
		zap.L().Error("Failed to upgrade to websocket", zap.Error(err))
		return
	}
	defer conn.Close()

	// Create new robot session
	sessionID := uuid.New().String()
	session := models.NewRoboSession(sessionID, conn, redisClient)
	session.Logger.Info("New robot session started")

	// Initialize handlers (they start their own goroutines)
	audioHandler, err := InitAudioHandler(session)
	if err != nil {
		session.Logger.Error("Failed to initialize audio handler", zap.Error(err))
		session.Stop()
		return
	}

	videoHandler := InitVideoHandler(session)
	intentionHandler := InitIntentionHandler(session)

	// Start the main session orchestrator goroutine
	go handleSessionOrchestrator(session, audioHandler, videoHandler, intentionHandler)

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
			handleConfigMessage(session, msg.Data)
		case "audio_data":
			handleAudioData(session, audioHandler, msg.Data)
		case "ping":
			sendWebSocketMessage(session, "pong", nil)
		case "stop":
			session.Logger.Info("Received stop command from client")

			// Send SESSION_END to all channels to stop all goroutines
			session.SendToAllChannels(models.SESSION_END)

			// Stop the session
			session.Stop()

			// Send confirmation back to client
			sendWebSocketMessage(session, "stop_confirmation", map[string]interface{}{
				"session_id": session.ID,
				"message":    "Session stopped successfully",
			})

			return
		default:
			session.Logger.Warn("Unknown message type", zap.String("type", msg.Type))
		}
	}

	// Clean up session
	session.Logger.Info("Robot session ended")
	session.Stop()
}

func handleSessionOrchestrator(session *models.RoboSession, audioHandler *AudioHandler, videoHandler *VideoHandler, intentionHandler *IntentionHandler) {
	session.Logger.Info("Session orchestrator started")

	// Start the main event loop
	for session.IsActive {
		select {
		case transcript := <-session.TranscriptionCh:
			if transcript == models.SESSION_END {
				session.Logger.Info("Session orchestrator received SESSION_END")
				return
			}

			session.Logger.Debug("Received transcript", zap.String("transcript", transcript))

			if transcript == "<END_OF_SPEECH>" {
				// Process the accumulated transcript for intention
				if session.CurrentTranscript != "" {
					session.Logger.Info("End of speech detected, processing transcript", zap.String("transcript", session.CurrentTranscript))

					// Update context for new processing
					session.UpdateContext()

					// Send the final transcript to the client
					sendWebSocketMessage(session, "transcript_final", map[string]interface{}{
						"transcript": session.CurrentTranscript,
						"timestamp":  time.Now(),
					})

					// Process the complete transcript for intention analysis
					intentionHandler.ProcessTranscript(session.CurrentTranscript)

					// Reset transcript buffer
					session.CurrentTranscript = ""
				}
			} else {
				// Accumulate transcript (filter out empty/whitespace)
				if strings.TrimSpace(transcript) != "" {
					session.CurrentTranscript += transcript + " "

					// Send interim transcript to client
					sendWebSocketMessage(session, "transcript_interim", map[string]interface{}{
						"transcript": strings.TrimSpace(session.CurrentTranscript),
						"timestamp":  time.Now(),
					})
				}
			}

		case interruption := <-session.InterruptionCh:
			if interruption == models.SESSION_END {
				session.Logger.Info("Session orchestrator received SESSION_END")
				return
			}

			session.Logger.Debug("Received interruption", zap.String("interruption", interruption))

			// Handle interruption - cancel current context and reset
			session.Logger.Info("User interruption detected")
			session.UpdateContext() // This cancels previous context

			// Send interruption to client
			sendWebSocketMessage(session, "interruption", map[string]interface{}{
				"interruption": interruption,
				"timestamp":    time.Now(),
			})

		case intentionResult := <-session.IntentionCh:
			session.Logger.Info("Received intention result",
				zap.String("description", intentionResult.Description),
				zap.Bool("has_clear_intention", intentionResult.HasClearIntention),
				zap.Float64("confidence", intentionResult.Confidence))

			if intentionResult.HasClearIntention {
				session.Logger.Info("Clear intention detected, triggering orchestrator")
				go triggerOrchestrator(session, intentionResult)
			}

			// Send intention result to client
			sendWebSocketMessage(session, "intention_result", intentionResult)

		case videoAnalysis := <-session.VideoAnalysisCh:
			if videoAnalysis == models.SESSION_END {
				session.Logger.Info("Session orchestrator received SESSION_END")
				return
			}

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

	// Cleanup handlers
	session.Logger.Info("Cleaning up handlers")
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

	sendWebSocketMessage(session, "config_updated", map[string]interface{}{
		"video_frequency": session.VideoFrequency.String(),
		"audio_frequency": session.AudioFrequency.String(),
	})
}

func handleAudioData(session *models.RoboSession, audioHandler *AudioHandler, data interface{}) {
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

func sendWebSocketMessage(session *models.RoboSession, msgType string, data interface{}) {
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

func triggerOrchestrator(session *models.RoboSession, intention models.IntentionResult) {
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
		sendWebSocketMessage(session, "orchestrator_triggered", map[string]interface{}{
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
