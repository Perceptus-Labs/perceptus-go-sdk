// handlers/intention_handler.go

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"github.com/pinecone-io/go-pinecone/v4/pinecone"
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

	return intentionHandler
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

	if hasIntention && confidence > 0.7 {
		go h.notifyOrchestrator(result)
	}

	h.session.sendWebSocketMessage("intention_analysis", result)
}

func (h *IntentionHandler) getRelevantEnvironmentContext(ctx context.Context, transcript string) ([]string, error) {
	if h.pineconeIdx == nil {
		return []string{}, nil
	}
	queryResponse, err := utils.FetchResponseFromPinecone(ctx, h.pineconeIdx, transcript)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch response from Pinecone: %w", err)
	}

	return queryResponse, nil
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
	h.session.Logger.Info("Orchestrator notification payload", zap.Any("payload", payload))

	jsonData, err := json.Marshal(payload)
	if err != nil {
		h.session.Logger.Error("Failed to marshal payload", zap.Error(err))
		return
	}
	resp, err := http.Post(os.Getenv("ORCHESTRATOR_URL"), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		h.session.Logger.Error("Failed to make API call to orchestrator", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.session.Logger.Error("Failed to read response body", zap.Error(err))
		return
	}

	h.session.Logger.Info("Orchestrator response", zap.String("body", string(body)))
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
	h.analyzeIntention(transcript)
}
