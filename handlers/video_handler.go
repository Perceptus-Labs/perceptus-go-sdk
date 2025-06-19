package handlers

import (
	"context"
	"encoding/base64"
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

	for h.isActive {
		b64 := <-h.session.VideoAnalysisCh
		if b64 == models.SESSION_END {
			h.session.Logger.Info("Video handler received SESSION_END")
			return
		}

		// 1) Decode
		imgBytes, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			h.session.Logger.Error("failed to decode video_data", zap.Error(err))
			continue
		}

		// 2) Send through your existing capture→analyze pipeline:
		go h.captureAndAnalyze(imgBytes)
	}
	h.session.Logger.Info("Video handler goroutine stopped")
}

func (h *VideoHandler) captureAndAnalyze(imageData []byte) {
	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h.session.Logger.Debug("Capturing and analyzing image")

	// Analyze image with OpenAI GPT-4V
	environmentSummary, err := h.openaiClient.AnalyzeImageContext(ctx, imageData)
	if err != nil {
		h.session.Logger.Error("Failed to analyze image", zap.Error(err))
		return
	}

	h.session.Logger.Debug("Generated environment description", zap.String("description", environmentSummary.Overview))

	// Create environment context
	envContext := models.EnvironmentContext{
		ID:             fmt.Sprintf("%s-%d", h.session.ID, time.Now().Unix()),
		SessionID:      h.session.ID,
		Timestamp:      time.Now(),
		ImageData:      imageData,
		Overview:       environmentSummary.Overview,
		KeyElements:    environmentSummary.KeyElements,
		Layout:         environmentSummary.Layout,
		Activities:     environmentSummary.Activities,
		AdditionalInfo: environmentSummary.AdditionalInfo,
	}
	// Store in Pinecone if available (async)
	if h.pineconeIdx != nil {
		go h.storeEnvironmentContext(envContext)
	}

	// Send to video analysis channel
	select {
	case h.session.VideoAnalysisCh <- fmt.Sprintf("%s", envContext):
		h.session.Logger.Debug("Sent video analysis to channel")
	default:
		h.session.Logger.Warn("Video analysis channel full, dropping analysis")
	}

	// Send analysis result via websocket
	// h.session.sendWebSocketMessage("video_analysis", analysis)
}

func (h *VideoHandler) storeEnvironmentContext(envContext models.EnvironmentContext) {
	if h.pineconeIdx == nil {
		return
	}

	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h.session.Logger.Debug("Storing environment context in Pinecone")

	// Create embeddings for each object and the description by converting the environment context to a string
	allTexts := fmt.Sprintf("%s", envContext)

	// Create embedding for the entire environment context
	embedding, err := utils.VectorizePrompt("text-embedding-ada-002", ctx, allTexts)
	if err != nil {
		h.session.Logger.Error("Failed to create embedding", zap.Error(err), zap.String("text", allTexts))
		return
	}

	// Create vector ID
	vectorID := fmt.Sprintf("%s-env", envContext.ID)

	// Prepare metadata
	metadata := map[string]interface{}{
		"text":            allTexts,
		"overview":        envContext.Overview,
		"key_elements":    envContext.KeyElements,
		"layout":          envContext.Layout,
		"activities":      envContext.Activities,
		"additional_info": envContext.AdditionalInfo,
		"session_id":      envContext.SessionID,
		"timestamp":       envContext.Timestamp.Unix(),
		"type":            "environment_context",
	}

	// Use the utility function to upsert to Pinecone
	err = utils.UpsertToPinecone(ctx, h.pineconeIdx, vectorID, embedding, metadata)
	if err != nil {
		h.session.Logger.Error("Failed to upsert to Pinecone", zap.Error(err), zap.String("vector_id", vectorID))
	}

	h.session.Logger.Debug("Environment context stored in Pinecone")
}

func (h *VideoHandler) Close() {
	h.session.Logger.Info("Closing Video Handler")
	h.isActive = false
}
