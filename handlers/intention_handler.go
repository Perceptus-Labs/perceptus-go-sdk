package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"github.com/pinecone-io/go-pinecone/pinecone"
	"go.uber.org/zap"
)

type IntentionHandler struct {
	session      *RoboSession
	openaiClient *utils.OpenAIClient
	pineconeIdx  *pinecone.IndexConnection
	isActive     bool
}

func InitIntentionHandler(session *RoboSession) *IntentionHandler {
	session.Logger.Info("Initializing Intention Handler...")

	// Initialize OpenAI client
	openaiClient := utils.NewOpenAIClient()

	// Initialize Pinecone connection
	pineconeIdx, err := utils.GetPineconeIndex(&session.ID)
	if err != nil {
		session.Logger.Warn("Failed to initialize Pinecone connection", zap.Error(err))
		// Continue without Pinecone - we'll still do intention analysis
	}

	intentionHandler := &IntentionHandler{
		session:      session,
		openaiClient: openaiClient,
		pineconeIdx:  pineconeIdx,
		isActive:     true,
	}

	session.Logger.Info("Intention Handler initialized")

	// Start the continuous intention processing goroutine
	go intentionHandler.run()

	return intentionHandler
}

func (h *IntentionHandler) run() {
	h.session.Logger.Info("Intention handler goroutine started")

	// The intention handler doesn't need to run continuously
	// It will be called directly when there's a transcript to analyze
	// Just wait for the session to end
	for h.isActive {
		time.Sleep(1 * time.Second)
	}

	h.session.Logger.Info("Intention handler goroutine stopped")
}

func (h *IntentionHandler) analyzeIntention(transcript string) {
	// Create a new context with timeout for this specific operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h.session.Logger.Debug("Analyzing intention from transcript", zap.String("transcript", transcript))

	// Get relevant environment context from Pinecone
	var environmentContext []string
	if h.pineconeIdx != nil {
		context, err := h.getRelevantEnvironmentContext(ctx, transcript)
		if err != nil {
			h.session.Logger.Error("Failed to get environment context", zap.Error(err))
		} else {
			environmentContext = context
		}
	}

	// Check if context was cancelled before proceeding with expensive API call
	select {
	case <-ctx.Done():
		h.session.Logger.Debug("Context cancelled before intention analysis")
		return
	default:
	}

	// Analyze intention with OpenAI
	intention, err := h.openaiClient.AnalyzeTranscriptForIntention(ctx, transcript, environmentContext)
	if err != nil {
		h.session.Logger.Error("Failed to analyze intention", zap.Error(err))
		return
	}

	// Parse the intention result
	hasIntention, intentionType, description, confidence := intention.HasClearIntention, intention.IntentionType, intention.Description, intention.Confidence

	// Create intention result
	result := models.IntentionResult{
		HasClearIntention:  hasIntention,
		IntentionType:      intentionType,
		Description:        description,
		Confidence:         confidence,
		EnvironmentContext: strings.Join(environmentContext, "\n"),
		Timestamp:          time.Now(),
	}

	// Log the intention result
	if hasIntention {
		h.session.Logger.Info("Intention detected",
			zap.String("type", intentionType),
			zap.String("description", description),
			zap.Float64("confidence", confidence))
	} else {
		h.session.Logger.Debug("No clear intention detected",
			zap.String("description", description),
			zap.Float64("confidence", confidence))
	}

	// If clear intention detected, make API call to orchestrator
	if hasIntention && confidence > 0.7 {
		go h.notifyOrchestrator(result)
	}

	// Send result via websocket (if needed)
	// h.session.sendWebSocketMessage("intention_analysis", result)
}

func (h *IntentionHandler) getRelevantEnvironmentContext(ctx context.Context, transcript string) ([]string, error) {
	if h.pineconeIdx == nil {
		return []string{}, nil
	}

	// Create embedding for the transcript
	embedding, err := utils.VectorizePrompt("text-embedding-ada-002", ctx, transcript)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Query Pinecone for similar environment contexts
	queryRequest := &pinecone.QueryByVectorValuesRequest{
		Vector:          embedding,
		TopK:            uint32(5),
		IncludeValues:   false,
		IncludeMetadata: true,
	}

	queryResponse, err := h.pineconeIdx.QueryByVectorValues(ctx, queryRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to query Pinecone: %w", err)
	}

	var contexts []string
	for _, match := range queryResponse.Matches {
		if match.Vector != nil && match.Vector.Metadata != nil {
			if value, ok := match.Vector.Metadata.Fields["text"]; ok {
				text := value.GetStringValue()
				if text != "" {
					contexts = append(contexts, text)
				}
			}
		}
	}

	return contexts, nil
}

func (h *IntentionHandler) notifyOrchestrator(result models.IntentionResult) {
	h.session.Logger.Info("Notifying orchestrator of detected intention",
		zap.String("type", result.IntentionType),
		zap.Float64("confidence", result.Confidence))

	// Prepare payload for orchestrator
	payload := map[string]interface{}{
		"session_id":          h.session.ID,
		"intention_type":      result.IntentionType,
		"description":         result.Description,
		"confidence":          result.Confidence,
		"transcript":          h.session.CurrentTranscript,
		"environment_context": result.EnvironmentContext,
		"timestamp":           result.Timestamp.Unix(),
	}

	// Make API call to orchestrator
	// This would be implemented based on your orchestrator's API
	// For now, we'll just log it
	h.session.Logger.Info("Orchestrator notification payload", zap.Any("payload", payload))

	// TODO: Implement actual API call to orchestrator
	// Example:
	// resp, err := http.Post("http://orchestrator/api/intention", "application/json", bytes.NewBuffer(jsonData))
}

func (h *IntentionHandler) Close() {
	h.session.Logger.Info("Closing Intention Handler")
	h.isActive = false
}

// ProcessTranscript should be called when a complete transcript is ready
func (h *IntentionHandler) ProcessTranscript(transcript string) {
	if transcript == "" {
		return
	}

	h.session.Logger.Info("Processing transcript for intention analysis", zap.String("transcript", transcript))

	// Analyze the intention directly
	h.analyzeIntention(transcript)
}
