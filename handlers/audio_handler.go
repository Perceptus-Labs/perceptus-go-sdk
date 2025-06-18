package handlers

import (
	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"go.uber.org/zap"
)

type AudioHandler struct {
	session        *models.RoboSession
	deepgramClient *utils.DeepgramClient
	isActive       bool
}

func InitAudioHandler(session *models.RoboSession) (*AudioHandler, error) {
	session.Logger.Info("Initializing Audio Handler...")

	// Initialize Deepgram client with default settings
	deepgramClient := utils.InitDeepgramClient(
		"en",  // Default language
		"0.6", // Default confidence threshold
		session.TranscriptionCh,
		session.InterruptionCh,
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

	return audioHandler, nil
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
