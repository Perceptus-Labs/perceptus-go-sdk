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
		"0.6", // Default confidence threshold
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
	go audioHandler.run()

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
				// h.session.sendWebSocketMessage("transcript_interim", map[string]interface{}{
				// 	"transcript": strings.TrimSpace(h.session.CurrentTranscript),
				// 	"timestamp":  time.Now(),
				// })
			}
		}
	}
}

func (h *AudioHandler) run() {
	h.session.Logger.Info("Audio handler goroutine started")

	for h.isActive {
		select {
		case transcript := <-h.session.TranscriptionCh:
			if transcript == models.SESSION_END {
				h.session.Logger.Info("Audio handler received SESSION_END")
				return
			}
			// Process transcript - this gets handled by the orchestrator
			h.session.Logger.Debug("Received transcript", zap.String("transcript", transcript))

		case interruption := <-h.session.InterruptionCh:
			if interruption == models.SESSION_END {
				h.session.Logger.Info("Audio handler received SESSION_END")
				return
			}
			// Process interruption - this gets handled by the orchestrator
			h.session.Logger.Debug("Received interruption", zap.String("interruption", interruption))

		case <-h.session.CurrentContext.Done():
			h.session.Logger.Debug("Audio handler context cancelled")
			// Don't exit, just wait for next message or SESSION_END
		}
	}

	h.session.Logger.Info("Audio handler goroutine stopped")
}

// ProcessAudioData sends audio data directly to Deepgram (called from WebSocket handler)
func (h *AudioHandler) ProcessAudioData(audioData []byte) error {
	if !h.isActive {
		return nil
	}

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
