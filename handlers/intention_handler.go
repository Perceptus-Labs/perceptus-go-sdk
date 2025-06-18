package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Perceptus-Labs/perceptus-go-sdk/models"
	"github.com/Perceptus-Labs/perceptus-go-sdk/utils"
	"github.com/pinecone-io/go-pinecone/pinecone"
	"go.uber.org/zap"
)

type IntentionHandler struct {
	session         *models.RoboSession
	openaiClient    *utils.OpenAIClient
	pineconeIdx     *pinecone.IndexConnection
	videoHandler    *VideoHandler
	isActive        bool
	processingMutex sync.Mutex
}

type IntentionAnalysisResult struct {
	HasClearIntention bool    `json:"has_clear_intention"`
	IntentionType     string  `json:"intention_type"`
	Description       string  `json:"description"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning"`
}

func InitIntentionHandler(session *models.RoboSession) *IntentionHandler {
	session.Logger.Info("Initializing Intention Handler...")

	// Initialize OpenAI client
	openaiClient := utils.NewOpenAIClient()

	// Initialize Pinecone connection
	pineconeIdx, err := utils.GetPineconeIndex(&session.ID)
	if err != nil {
		session.Logger.Warn("Failed to initialize Pinecone connection", zap.Error(err))
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

func (h *IntentionHandler) SetVideoHandler(videoHandler *VideoHandler) {
	h.videoHandler = videoHandler
}

func (h *IntentionHandler) StartIntentionProcessor() {
	h.session.Logger.Info("Started intention processor")

	// The intention processor will be triggered by ProcessTranscriptForIntention
	// This method just keeps the handler alive
	for h.isActive {
		select {
		case <-h.session.CurrentContext.Done():
			h.session.Logger.Info("Intention processor stopped - context cancelled")
			return
		case <-time.After(30 * time.Second):
			// Periodic heartbeat
			h.session.Logger.Debug("Intention processor heartbeat")
		}
	}
}

func (h *IntentionHandler) ProcessTranscriptForIntention(transcript string) {
	h.processingMutex.Lock()
	defer h.processingMutex.Unlock()

	ctx, cancel := context.WithTimeout(h.session.CurrentContext, 45*time.Second)
	defer cancel()

	h.session.Logger.Info("Processing transcript for intention analysis", zap.String("transcript", transcript))

	// Step 1: Get current environment context from video analysis
	var videoContext string
	if h.videoHandler != nil {
		contextDesc, err := h.videoHandler.CaptureImageForContext(transcript)
		if err != nil {
			h.session.Logger.Warn("Failed to get video context, continuing without it", zap.Error(err))
		} else {
			videoContext = contextDesc
		}
	}

	// Step 2: Retrieve relevant environment context from Pinecone
	var pineconeContext []string
	if h.pineconeIdx != nil {
		relevantContext, err := h.getRelevantEnvironmentContext(ctx, transcript)
		if err != nil {
			h.session.Logger.Warn("Failed to get Pinecone context", zap.Error(err))
		} else {
			pineconeContext = relevantContext
		}
	}

	// Step 3: Combine all environment context
	allContext := pineconeContext
	if videoContext != "" {
		allContext = append(allContext, fmt.Sprintf("Current visual context: %s", videoContext))
	}

	// Step 4: Analyze transcript for intention using OpenAI
	intentionJSON, err := h.openaiClient.AnalyzeTranscriptForIntention(ctx, transcript, allContext)
	if err != nil {
		h.session.Logger.Error("Failed to analyze transcript for intention", zap.Error(err))
		return
	}

	// Step 5: Parse the intention analysis result
	var analysisResult IntentionAnalysisResult
	err = json.Unmarshal([]byte(intentionJSON), &analysisResult)
	if err != nil {
		h.session.Logger.Error("Failed to parse intention analysis JSON", zap.Error(err))
		return
	}

	// Step 6: Create intention result
	intentionResult := models.IntentionResult{
		HasClearIntention:  analysisResult.HasClearIntention,
		IntentionType:      analysisResult.IntentionType,
		Description:        analysisResult.Description,
		Confidence:         analysisResult.Confidence,
		EnvironmentContext: allContext,
		TranscriptAnalysis: analysisResult.Reasoning,
		Timestamp:          time.Now(),
	}

	h.session.Logger.Info("Intention analysis completed",
		zap.Bool("has_intention", intentionResult.HasClearIntention),
		zap.Float64("confidence", intentionResult.Confidence),
		zap.String("description", intentionResult.Description))

	// Step 7: Send result to intention channel
	select {
	case h.session.IntentionCh <- intentionResult:
		h.session.Logger.Debug("Sent intention result to channel")
	default:
		h.session.Logger.Warn("Intention channel full, dropping result")
	}
}

func (h *IntentionHandler) getRelevantEnvironmentContext(ctx context.Context, transcript string) ([]string, error) {
	if h.pineconeIdx == nil {
		return nil, fmt.Errorf("pinecone index not available")
	}

	h.session.Logger.Debug("Retrieving relevant environment context from Pinecone")

	// Get relevant context from Pinecone using the transcript
	relevantContext, err := utils.FetchResponseFromPinecone(ctx, h.pineconeIdx, transcript)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from Pinecone: %w", err)
	}

	h.session.Logger.Debug("Retrieved environment context from Pinecone", zap.Int("count", len(relevantContext)))
	return relevantContext, nil
}

func (h *IntentionHandler) Close() {
	h.session.Logger.Info("Closing Intention Handler")
	h.isActive = false
}
