// handlers/audio_handler.go

package handlers

import (
	"strings"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"go.uber.org/zap"
)

type AudioHandler struct {
	session        *RoboSession
	deepgramClient *utils.DeepgramClient
	isActive       bool
}

func InitAudioHandler(session *RoboSession) (*AudioHandler, error) {
	session.Logger.Info("Initializing Audio Handler...")

	// Initialize Deepgram client with default settings
	deepgramClient := utils.InitDeepgramClient(
		"en",  // Default language
		"0.3", // Default confidence threshold
		session.TranscriptionCh,
	)

	// Connect to Deepgram
	deepgramClient.Connect()

	audioHandler := &AudioHandler{
		session:        session,
		deepgramClient: deepgramClient,
		isActive:       true,
	}

	session.Logger.Info("Audio Handler initialized and connected to Deepgram")

	// Start the handler goroutine to listen for SESSION_END
	go audioHandler.handleTranscript()

	return audioHandler, nil
}

func (h *AudioHandler) handleTranscript() {
	for h.session.IsActive {
		transcript := <-h.session.TranscriptionCh
		if transcript == models.SESSION_END {
			h.session.Logger.Info("Session orchestrator received SESSION_END")
			return
		}

		h.session.Logger.Debug("Received transcript", zap.String("transcript", transcript))

		if transcript == "<END_OF_SPEECH>" {
			// Process the accumulated transcript for intention
			if h.session.CurrentTranscript != "" {
				h.session.Logger.Info("End of speech detected, processing transcript", zap.String("transcript", h.session.CurrentTranscript))
				h.session.sendWebSocketMessage("transcript_final", map[string]string{
					"transcript": transcript,
				})
				// Update context for new processing
				h.session.UpdateContext()

				// Send the final transcript to the client
				// h.session.sendWebSocketMessage("transcript_final", map[string]interface{}{
				// 	"transcript": h.session.CurrentTranscript,
				// 	"timestamp":  time.Now(),
				// })

				// Process the complete transcript for intention analysis
				h.session.IntentionHandler.ProcessTranscript(h.session.CurrentTranscript)

				// Reset transcript buffer
				h.session.CurrentTranscript = ""
			}
		} else {
			// Accumulate transcript (filter out empty/whitespace)
			if strings.TrimSpace(transcript) != "" {
				h.session.CurrentTranscript += transcript + " "

				// Send interim transcript to client
				h.session.sendWebSocketMessage("transcript_interim", map[string]string{
					"transcript": strings.TrimSpace(h.session.CurrentTranscript),
				})
			}
		}
	}
}

// ProcessAudioData sends audio data directly to Deepgram (called from WebSocket handler)
func (h *AudioHandler) ProcessAudioData(audioData []byte) error {
	// Send audio data to Deepgram immediately
	err := h.deepgramClient.Send(audioData)
	if err != nil {
		h.session.Logger.Error("Failed to send audio data to Deepgram", zap.Error(err))
		return err
	}

	return nil
}

func (h *AudioHandler) Close() {
	h.session.Logger.Info("Closing Audio Handler")
	h.isActive = false

	if h.deepgramClient != nil {
		h.deepgramClient.Close()
	}
}
