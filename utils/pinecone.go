package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"

	"github.com/pinecone-io/go-pinecone/pinecone"
)

func GetPineconeIndex(perceptusID *string) (*pinecone.IndexConnection, error) {
	ctx := context.Background()
	if perceptusID == nil {
		return nil, nil
	}

	indexName := os.Getenv("PINECONE_INDEX")
	if indexName == "" {
		return nil, fmt.Errorf("PINECONE_INDEX environment variable is not set")
	}

	pineconeAPIKey := os.Getenv("PINECONE_API_KEY")
	if pineconeAPIKey == "" {
		return nil, fmt.Errorf("PINECONE_API_KEY environment variable is not set")
	}

	clientParams := pinecone.NewClientParams{
		ApiKey: pineconeAPIKey,
	}

	client, err := pinecone.NewClient(clientParams)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pinecone client: %w", err)
	}

	idx, err := client.DescribeIndex(ctx, indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to describe index \"%v\": %v", idx.Name, err)
	}

	namespace := fmt.Sprintf("perceptus-%s", *perceptusID)
	idxConnection, err := client.Index(pinecone.NewIndexConnParams{Host: idx.Host, Namespace: namespace})
	if err != nil {
		return nil, fmt.Errorf("failed to create IndexConnection for Host %v: %v", idx.Host, err)
	}

	return idxConnection, nil
}

func FetchResponseFromPinecone(ctx context.Context, index *pinecone.IndexConnection, promptText string) ([]string, error) {
	embededText, err := VectorizePrompt("text-embedding-ada-002", ctx, promptText)
	if err != nil {
		return nil, fmt.Errorf("error vectorizing prompt: %w", err)
	}
	ragResponse, err := QueryPinecone(ctx, embededText, index, 5)
	if err != nil {
		return nil, fmt.Errorf("error querying Pinecone index: %w", err)
	}
	return ragResponse, nil
}

func QueryPinecone(ctx context.Context, embedding []float32, index *pinecone.IndexConnection, topK int) ([]string, error) {
	// Prepare the query request
	queryRequest := &pinecone.QueryByVectorValuesRequest{
		Vector:          embedding,
		TopK:            uint32(topK),
		IncludeValues:   false,
		IncludeMetadata: true,
	}

	// Perform the query
	queryResponse, err := index.QueryByVectorValues(ctx, queryRequest)
	if err != nil {
		return nil, fmt.Errorf("error querying Pinecone index: %w", err)
	}

	// Extract the matches
	var matches []string
	for _, match := range queryResponse.Matches {
		if match.Vector == nil || match.Vector.Metadata == nil {
			continue
		}

		// Extract the 'text' field from metadata.
		value, ok := match.Vector.Metadata.Fields["text"]
		if ok {
			text := value.GetStringValue()
			if text != "" {
				matches = append(matches, text)
			}
		}
	}

	return matches, nil
}

func VectorizePrompt(model string, ctx context.Context, promptText string) ([]float32, error) {
	openAIAPIKey := os.Getenv("OPENAI_API_KEY")
	if openAIAPIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	// Prepare the request payload
	requestBody := map[string]interface{}{
		"input": promptText,
		"model": model,
	}
	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create the HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(requestBodyBytes))
	if err != nil && !errors.Is(err, errors.New("context canceled")) {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+openAIAPIKey)

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for errors in the response
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse the response
	var responseData struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response JSON: %w", err)
	}

	if len(responseData.Data) == 0 {
		return nil, fmt.Errorf("no data in OpenAI API response")
	}

	embedding := responseData.Data[0].Embedding
	return embedding, nil
}

// CosineSimilarity, using the angle between two vectors to determine similarity
// CosineSimilarity, compared to DotProduct, is more robust to changes in magnitude
// This means for two text embeddings, cosine similarity will explain more similarity semantically
func CosineSimilarity(vec1, vec2 []float32) float32 {
	if len(vec1) != len(vec2) || len(vec1) == 0 {
		return 0
	}

	var dotProduct float32
	var norm1 float32
	var norm2 float32

	for i := 0; i < len(vec1); i++ {
		dotProduct += vec1[i] * vec2[i]
		norm1 += vec1[i] * vec1[i]
		norm2 += vec2[i] * vec2[i]
	}

	norm1 = float32(math.Sqrt(float64(norm1)))
	norm2 = float32(math.Sqrt(float64(norm2)))

	if norm1 == 0 || norm2 == 0 {
		return 0 // Handle zero vectors to avoid division by zero
	}

	return dotProduct / (norm1 * norm2)
}

// DotProduct, using the magnitude of two vectors to determine similarity
// DotProduct, compared to CosineSimilarity, is more sensitive to changes in magnitude
// This means for two text embeddings, dot product will explain more similarity in terms of identical words
func DotProduct(vec1, vec2 []float32) float32 {
	if len(vec1) != len(vec2) || len(vec1) == 0 {
		return 0
	}

	var dotProduct float32
	for i := 0; i < len(vec1); i++ {
		dotProduct += vec1[i] * vec2[i]
	}

	return dotProduct
}
