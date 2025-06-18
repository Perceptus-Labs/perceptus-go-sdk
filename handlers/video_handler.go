package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"github.com/pinecone-io/go-pinecone/pinecone"
)

type VideoHandler struct {
	session      *models.RoboSession
	camera       *utils.CameraCapture
	openaiClient *utils.OpenAIClient
	pineconeIdx  *pinecone.IndexConnection
	isActive     bool
}

func InitVideoHandler(session *models.RoboSession) *VideoHandler {
	session.Logger.Info("Initializing Video Handler...")

	// Initialize camera
	camera := utils.NewCameraCapture()

	// Initialize OpenAI client
	openaiClient := utils.NewOpenAIClient()

	// Initialize Pinecone connection
	pineconeIdx, err := utils.GetPineconeIndex(&session.ID)
	if err != nil {
		session.Logger.WithError(err).Warn("Failed to initialize Pinecone connection")
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
	return videoHandler
}

func (h *VideoHandler) StartPeriodicCapture() {
	h.session.Logger.Info("Started periodic video capture", "frequency", h.session.VideoFrequency)

	ticker := time.NewTicker(h.session.VideoFrequency)
	defer ticker.Stop()

	for h.isActive {
		select {
		case <-ticker.C:
			go h.captureAndAnalyze()

		case <-h.session.CurrentContext.Done():
			h.session.Logger.Info("Periodic video capture stopped - context cancelled")
			return
		}
	}
}

func (h *VideoHandler) captureAndAnalyze() {
	ctx, cancel := context.WithTimeout(h.session.CurrentContext, 30*time.Second)
	defer cancel()

	h.session.Logger.Debug("Capturing and analyzing image")

	// Capture image from camera
	imageData, err := h.camera.TryCapture()
	if err != nil {
		h.session.Logger.WithError(err).Error("Failed to capture image")
		return
	}

	// Analyze image with OpenAI GPT-4V
	description, err := h.openaiClient.GenerateEnvironmentDescription(ctx, imageData)
	if err != nil {
		h.session.Logger.WithError(err).Error("Failed to analyze image")
		return
	}

	h.session.Logger.Debug("Generated environment description:", description)

	// Create environment context
	envContext := models.EnvironmentContext{
		ID:          fmt.Sprintf("%s-%d", h.session.ID, time.Now().Unix()),
		SessionID:   h.session.ID,
		Description: description,
		Objects:     h.extractObjects(description),
		Timestamp:   time.Now(),
		ImageData:   imageData,
	}

	// Create video analysis result
	analysis := models.VideoAnalysis{
		SessionID:          h.session.ID,
		Description:        description,
		KeyObjects:         envContext.Objects,
		EnvironmentSummary: description,
		Timestamp:          time.Now(),
	}

	// Store in Pinecone if available
	if h.pineconeIdx != nil {
		go h.storeEnvironmentContext(envContext)
	}

	// Send to video analysis channel
	select {
	case h.session.VideoAnalysisCh <- fmt.Sprintf("Environment analysis: %s", description):
		h.session.Logger.Debug("Sent video analysis to channel")
	default:
		h.session.Logger.Warn("Video analysis channel full, dropping analysis")
	}

	// Send analysis result via websocket
	sendWebSocketMessage(h.session, "video_analysis", analysis)
}

func (h *VideoHandler) CaptureImageForContext(transcript string) (string, error) {
	ctx, cancel := context.WithTimeout(h.session.CurrentContext, 15*time.Second)
	defer cancel()

	h.session.Logger.Info("Capturing image for context analysis")

	// Capture image
	imageData, err := h.camera.TryCapture()
	if err != nil {
		return "", fmt.Errorf("failed to capture image: %w", err)
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
	description, err := h.openaiClient.AnalyzeImage(ctx, imageData, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to analyze image: %w", err)
	}

	h.session.Logger.Debug("Generated contextual environment description")

	// Store this context in Pinecone as well
	if h.pineconeIdx != nil {
		envContext := models.EnvironmentContext{
			ID:          fmt.Sprintf("%s-context-%d", h.session.ID, time.Now().Unix()),
			SessionID:   h.session.ID,
			Description: fmt.Sprintf("Context for transcript: '%s' - %s", transcript, description),
			Objects:     h.extractObjects(description),
			Timestamp:   time.Now(),
			ImageData:   imageData,
		}
		go h.storeEnvironmentContext(envContext)
	}

	return description, nil
}

func (h *VideoHandler) extractObjects(description string) []string {
	// Simple object extraction from description
	// This could be made more sophisticated with NLP
	objects := []string{}

	// Convert to lowercase for easier matching
	desc := strings.ToLower(description)

	// Common objects to look for
	commonObjects := []string{
		"table", "chair", "couch", "sofa", "bed", "desk", "book", "laptop", "computer",
		"phone", "cup", "mug", "glass", "plate", "bottle", "keys", "remote", "tv",
		"light", "lamp", "door", "window", "kitchen", "bathroom", "bedroom", "living room",
		"person", "man", "woman", "child", "dog", "cat", "coffee", "water", "food",
	}

	for _, obj := range commonObjects {
		if strings.Contains(desc, obj) {
			objects = append(objects, obj)
		}
	}

	return objects
}

func (h *VideoHandler) storeEnvironmentContext(envContext models.EnvironmentContext) {
	if h.pineconeIdx == nil {
		return
	}

	ctx, cancel := context.WithTimeout(h.session.CurrentContext, 10*time.Second)
	defer cancel()

	h.session.Logger.Debug("Storing environment context in Pinecone")

	// Create embeddings for each object and the description
	allTexts := append(envContext.Objects, envContext.Description)

	for i, text := range allTexts {
		// Create embedding
		embedding, err := utils.VectorizePrompt("text-embedding-ada-002", ctx, text)
		if err != nil {
			h.session.Logger.WithError(err).Error("Failed to create embedding for:", text)
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

		if i < len(envContext.Objects) {
			metadata["object_type"] = "object"
			metadata["object_name"] = text
		} else {
			metadata["object_type"] = "description"
		}

		// Use the utility function to upsert to Pinecone
		err = utils.UpsertToPinecone(ctx, h.pineconeIdx, vectorID, embedding, metadata)
		if err != nil {
			h.session.Logger.WithError(err).Error("Failed to upsert to Pinecone:", vectorID)
		}
	}

	h.session.Logger.Debug("Environment context stored in Pinecone")
}

func (h *VideoHandler) Close() {
	h.session.Logger.Info("Closing Video Handler")
	h.isActive = false
}
