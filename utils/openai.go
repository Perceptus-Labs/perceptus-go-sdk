package utils

import (
	"bytes"
	"context"
	"encoding/base64"
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

func (c *OpenAIClient) AnalyzeImage(ctx context.Context, imageData []byte, prompt string) (*models.IntentionResult, error) {
	// Convert image to base64
	base64Image := base64.StdEncoding.EncodeToString(imageData)
	imageURL := fmt.Sprintf("data:image/jpeg;base64,%s", base64Image)

	// Prepare the message with image
	content := []ImageContent{
		{
			Type: "text",
			Text: prompt,
		},
		{
			Type: "image_url",
			ImageURL: &struct {
				URL string `json:"url"`
			}{
				URL: imageURL,
			},
		},
	}

	messages := []GPTMessage{
		{
			Role:    "user",
			Content: content,
		},
	}

	requestBody := map[string]interface{}{
		"model":      "gpt-4o",
		"messages":   messages,
		"max_tokens": 1000,
	}

	return c.sendRequest(ctx, requestBody)
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

func (c *OpenAIClient) GenerateEnvironmentDescription(ctx context.Context, imageData []byte) (*models.IntentionResult, error) {
	prompt := `Analyze this image and provide a detailed description of the environment for a robot assistant. Focus on:

1. Key objects and their locations (be specific about positioning)
2. Room type and layout
3. People present (if any)
4. Furniture and fixtures
5. Any items that could be relevant for robot tasks
6. Overall scene context

Format your response as a structured list of observations that would be useful for a robot to understand the environment and help with potential tasks. Be detailed but concise.

Example format:
- Room: Kitchen area with modern appliances
- Objects: Coffee mug on counter (left side), smartphone on table (center), keys near door
- People: One person standing near the refrigerator
- Furniture: Wooden dining table with 4 chairs, granite countertop
- Notable items: Fruit bowl with apples and bananas, coffee maker (right side of counter)
- Context: Person appears to be preparing breakfast`

	return c.AnalyzeImage(ctx, imageData, prompt)
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
			HasClearIntention:  false,
			IntentionType:      "unknown",
			Description:        content,
			Confidence:         0.0,
			EnvironmentContext: content,
			Timestamp:          time.Now(),
		}
	}

	return &intentionResult, nil
}
