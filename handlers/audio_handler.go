package handlers

import (
	"time"

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

	// Start processing audio
	go audioHandler.startAudioProcessing()

	return audioHandler, nil
}

func (h *AudioHandler) startAudioProcessing() {
	h.session.Logger.Info("Started audio processing")

	// This is where we would handle incoming audio data
	// For now, we'll just log that audio processing is active
	ticker := time.NewTicker(h.session.AudioFrequency)
	defer ticker.Stop()

	for h.isActive {
		select {
		case <-ticker.C:
			// Audio processing tick - this is where real audio processing would happen
			// For now, we just maintain the connection
			h.session.Logger.Debug("Audio processing tick")

		case <-h.session.CurrentContext.Done():
			h.session.Logger.Info("Audio processing stopped - context cancelled")
			return
		}
	}
}

func (h *AudioHandler) ProcessAudioData(audioData []byte) error {
	if !h.isActive {
		return nil
	}

	// Send audio data to Deepgram
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
