package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"github.com/pinecone-io/go-pinecone/pinecone"
	"go.uber.org/zap"
)

type VideoHandler struct {
	session      *RoboSession
	camera       *utils.CameraCapture
	openaiClient *utils.OpenAIClient
	pineconeIdx  *pinecone.IndexConnection
	isActive     bool
}

func InitVideoHandler(session *RoboSession) *VideoHandler {
	session.Logger.Info("Initializing Video Handler...")

	// Initialize camera
	camera := utils.NewCameraCapture()

	// Initialize OpenAI client
	openaiClient := utils.NewOpenAIClient()

	// Initialize Pinecone connection
	pineconeIdx, err := utils.GetPineconeIndex(&session.ID)
	if err != nil {
		session.Logger.Warn("Failed to initialize Pinecone connection", zap.Error(err))
		// Continue without Pinecone - we'll still do video analysis
	}

	videoHandler := &VideoHandler{
		session:      session,
		camera:       camera,
		openaiClient: openaiClient,
		pineconeIdx:  pineconeIdx,
		isActive:     true,
	}

	session.Logger.Info("Video Handler initialized")

	// Start the continuous video processing goroutine
	go videoHandler.run()

	return videoHandler
}

func (h *VideoHandler) run() {
	h.session.Logger.Info("Video handler goroutine started", zap.Duration("frequency", h.session.VideoFrequency))

	ticker := time.NewTicker(h.session.VideoFrequency)
	defer ticker.Stop()

	for h.isActive {
		select {
		case videoAnalysis := <-h.session.VideoAnalysisCh:
			if videoAnalysis == models.SESSION_END {
				h.session.Logger.Info("Video handler received SESSION_END")
				return
			}
			// Process video analysis (handled by orchestrator)

		case <-ticker.C:
			// Periodic video capture and analysis
			go h.captureAndAnalyze()
		}
	}

	h.session.Logger.Info("Video handler goroutine stopped")
}

func (h *VideoHandler) captureAndAnalyze() {
	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h.session.Logger.Debug("Capturing and analyzing image")

	// Capture image from camera
	imageData, err := h.camera.TryCapture()
	if err != nil {
		h.session.Logger.Error("Failed to capture image", zap.Error(err))
		return
	}

	// Check if context was cancelled before proceeding with expensive API call
	select {
	case <-ctx.Done():
		h.session.Logger.Debug("Context cancelled before image analysis")
		return
	default:
	}

	// Analyze image with OpenAI GPT-4V
	intentionResult, err := h.openaiClient.GenerateEnvironmentDescription(ctx, imageData)
	if err != nil {
		h.session.Logger.Error("Failed to analyze image", zap.Error(err))
		return
	}

	h.session.Logger.Debug("Generated environment description", zap.String("description", intentionResult.Description))

	// Create environment context
	envContext := models.EnvironmentContext{
		ID:          fmt.Sprintf("%s-%d", h.session.ID, time.Now().Unix()),
		SessionID:   h.session.ID,
		Description: intentionResult.Description,
		Timestamp:   time.Now(),
		ImageData:   imageData,
	}

	// Create video analysis result
	analysis := models.VideoAnalysis{
		SessionID:          h.session.ID,
		Description:        intentionResult.Description,
		EnvironmentSummary: intentionResult.Description,
		Timestamp:          time.Now(),
	}

	// Store in Pinecone if available (async)
	if h.pineconeIdx != nil {
		go h.storeEnvironmentContext(envContext)
	}

	// Send to video analysis channel
	select {
	case h.session.VideoAnalysisCh <- fmt.Sprintf("%s", analysis):
		h.session.Logger.Debug("Sent video analysis to channel")
	default:
		h.session.Logger.Warn("Video analysis channel full, dropping analysis")
	}

	// Send analysis result via websocket
	// h.session.sendWebSocketMessage("video_analysis", analysis)
}

func (h *VideoHandler) CaptureImageForContext(transcript string) (string, error) {
	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h.session.Logger.Info("Capturing image for context analysis")

	// Capture image
	imageData, err := h.camera.TryCapture()
	if err != nil {
		return "", fmt.Errorf("failed to capture image: %w", err)
	}

	// Check if context was cancelled before proceeding with expensive API call
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context cancelled before image analysis")
	default:
	}

	// Create a more specific prompt based on the transcript
	prompt := fmt.Sprintf(`Analyze this image to understand the environment that would help interpret what the user said: "%s"

Focus on identifying:
1. Objects and items that the user might be referring to
2. People and their activities
3. Room layout and furniture that's relevant to the user's statement
4. Any contextual clues that would help understand the user's intent

Provide a detailed but concise description focusing on elements that would be most relevant to understanding what the user wants or is talking about.`, transcript)

	// Analyze with OpenAI
	intentionResult, err := h.openaiClient.AnalyzeImage(ctx, imageData, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to analyze image: %w", err)
	}

	h.session.Logger.Debug("Generated contextual environment description", zap.String("description", intentionResult.Description))

	// Store this context in Pinecone as well (async)
	if h.pineconeIdx != nil {
		envContext := models.EnvironmentContext{
			ID:          fmt.Sprintf("%s-context-%d", h.session.ID, time.Now().Unix()),
			SessionID:   h.session.ID,
			Description: fmt.Sprintf("Context for transcript: '%s' - %s", transcript, intentionResult.Description),
			Timestamp:   time.Now(),
			ImageData:   imageData,
		}
		go h.storeEnvironmentContext(envContext)
	}

	return intentionResult.Description, nil
}

func (h *VideoHandler) storeEnvironmentContext(envContext models.EnvironmentContext) {
	if h.pineconeIdx == nil {
		return
	}

	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h.session.Logger.Debug("Storing environment context in Pinecone")

	// Create embeddings for each object and the description
	allTexts := []string{envContext.Description}

	for i, text := range allTexts {
		// Check if context was cancelled before proceeding
		select {
		case <-ctx.Done():
			h.session.Logger.Debug("Context cancelled during Pinecone storage")
			return
		default:
		}

		// Create embedding
		embedding, err := utils.VectorizePrompt("text-embedding-ada-002", ctx, text)
		if err != nil {
			h.session.Logger.Error("Failed to create embedding", zap.Error(err), zap.String("text", text))
			continue
		}

		// Create vector ID
		vectorID := fmt.Sprintf("%s-obj-%d", envContext.ID, i)

		// Prepare metadata
		metadata := map[string]interface{}{
			"text":       text,
			"session_id": envContext.SessionID,
			"timestamp":  envContext.Timestamp.Unix(),
			"type":       "environment_context",
		}

		// Use the utility function to upsert to Pinecone
		err = utils.UpsertToPinecone(ctx, h.pineconeIdx, vectorID, embedding, metadata)
		if err != nil {
			h.session.Logger.Error("Failed to upsert to Pinecone", zap.Error(err), zap.String("vector_id", vectorID))
		}
	}

	h.session.Logger.Debug("Environment context stored in Pinecone")
}

func (h *VideoHandler) Close() {
	h.session.Logger.Info("Closing Video Handler")
	h.isActive = false
}
