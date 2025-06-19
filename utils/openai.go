package utils

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
	"go.uber.org/zap"
)

type OpenAIClient struct {
	APIKey string
	Client *http.Client
}

type GPTMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type GPTResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type ImageContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

func NewOpenAIClient() *OpenAIClient {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		zap.L().Fatal("OPENAI_API_KEY environment variable not set")
	}

	return &OpenAIClient{
		APIKey: apiKey,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *OpenAIClient) AnalyzeTranscriptForIntention(ctx context.Context, transcript string, environmentContext []string) (*models.IntentionResult, error) {
	contextStr := ""
	if len(environmentContext) > 0 {
		contextStr = "Current environment context:\n" + strings.Join(environmentContext, "\n") + "\n\n"
	}

	prompt := fmt.Sprintf(`%sAnalyze the following transcript to determine if the user has expressed a clear intention for the robot to perform a task.

Transcript: "%s"

Please analyze this transcript and respond with a JSON object containing:
- "has_clear_intention": boolean indicating if there's a clear actionable intention
- "intention_type": string describing the type of intention (e.g., "navigation", "manipulation", "information_gathering", etc.)
- "description": string with a detailed description of what the user wants
- "confidence": float between 0 and 1 indicating confidence in the analysis
- "reasoning": string explaining your analysis

Examples of clear intentions:
- "Go to the kitchen and bring me a glass of water"
- "Move to the living room"
- "Pick up that book on the table"
- "Turn on the lights in the bedroom"

Examples of unclear/no intentions:
- "The weather is nice today"
- "I'm feeling tired"
- "What time is it?"
- General conversation without specific requests

Return the JSON object only, no other text.
Return in the following format:
{
	"has_clear_intention": boolean,
	"intention_type": string,
	"description": string,
	"confidence": float,
	"reasoning": string
}

Be conservative - only mark as clear intention if the user is explicitly asking the robot to do something specific.`, contextStr, transcript)

	messages := []GPTMessage{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	requestBody := map[string]interface{}{
		"model":    "gpt-4.1-2025-04-14",
		"messages": messages,
	}

	return c.sendRequest(ctx, requestBody)
}

// AnalyzeImageContext requests a detailed, structured, holistic context description.
func (c *OpenAIClient) AnalyzeImageContext(ctx context.Context, imageData string) (*models.EnvironmentContext, error) {
	// 1) Base64 encode the image
	dataURI := "data:image/jpeg;base64," + imageData

	// 2) System prompt to enforce JSON-only output with desired fields
	systemPrompt := `You are a vision-enabled assistant. Return ONLY a JSON object with key: overview (string), key_elements (array of strings), layout (string), activities (array of strings), additional_info (object of string pairs). No extra keys or prose.`

	// 3) User message including the image URI
	userPrompt := fmt.Sprintf("Analyze the scene depicted by the image below and output a structured JSON context description. IMAGE_URI:%s", dataURI)

	// 4) Build request body
	payload := map[string]interface{}{
		"model": "gpt-4o", // vision-enabled model
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens": 500,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error: %s", string(b))
	}

	// 5) Decode response
	var raw struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := raw.Choices[0].Message.Content
	clean := strings.TrimSpace(content)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimSuffix(clean, "```")
	zap.L().Debug("OpenAI context JSON", zap.String("content", content))

	// 6) Unmarshal into our struct
	var ctxDesc models.EnvironmentContext
	if err := json.Unmarshal([]byte(clean), &ctxDesc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context JSON: %w", err)
	}

	return &ctxDesc, nil
}

func (c *OpenAIClient) sendRequest(ctx context.Context, requestBody map[string]interface{}) (*models.IntentionResult, error) {
	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var response GPTResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response JSON: %w", err)
	}

	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI API response")
	}

	content := response.Choices[0].Message.Content
	zap.L().Debug("OpenAI response content", zap.String("content", content))

	// Try to parse as JSON first
	var intentionResult models.IntentionResult
	if err := json.Unmarshal([]byte(content), &intentionResult); err != nil {
		// If JSON parsing fails, create a default result with the raw content
		zap.L().Warn("Failed to parse OpenAI response as JSON, using raw content",
			zap.Error(err),
			zap.String("content", content))

		// Create a default intention result with the raw content
		intentionResult = models.IntentionResult{
			HasClearIntention:  intentionResult.HasClearIntention,
			IntentionType:      intentionResult.IntentionType,
			Description:        intentionResult.Description,
			Confidence:         intentionResult.Confidence,
			EnvironmentContext: intentionResult.EnvironmentContext,
			Timestamp:          time.Now(),
		}
	}

	return &intentionResult, nil
}
