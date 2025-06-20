package utils

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pinecone-io/go-pinecone/v4/pinecone"
)

func GetPineconeIndex(perceptusID *string) (*pinecone.IndexConnection, error) {
	pc, err := pinecone.NewClient(pinecone.NewClientParams{
		ApiKey: os.Getenv("PINECONE_API_KEY"),
	})
	if err != nil {
		log.Fatalf("Failed to create Client: %v", err)
	}
	idxConnection, err := pc.Index(pinecone.NewIndexConnParams{Host: os.Getenv("PINECONE_HOST"), Namespace: os.Getenv("PINECONE_NAMESPACE")})
	if err != nil {
		log.Fatalf("Failed to create IndexConnection for Host: %v", err)
	}

	return idxConnection, nil
}

func FetchResponseFromPinecone(ctx context.Context, index *pinecone.IndexConnection, promptText string) ([]string, error) {
	// Use text-based search with integrated embeddings
	// No need to manually vectorize the prompt text
	ragResponse, err := QueryPinecone(ctx, promptText, index, 5)
	if err != nil {
		return nil, fmt.Errorf("error querying Pinecone index: %w", err)
	}
	return ragResponse, nil
}

func QueryPinecone(ctx context.Context, queryText string, index *pinecone.IndexConnection, topK int) ([]string, error) {
	// Use text-based search with integrated embeddings
	// Pinecone will automatically convert the query text to a vector

	res, err := index.SearchRecords(ctx, &pinecone.SearchRecordsRequest{
		Query: pinecone.SearchRecordsQuery{
			TopK: int32(topK),
			Inputs: &map[string]interface{}{
				"text": queryText,
			},
		},
		Fields: &[]string{"chunk_text", "category"},
	})
	if err != nil {
		return nil, fmt.Errorf("error searching Pinecone index: %w", err)
	}

	// Extract the matches
	var matches []string
	for _, hit := range res.Result.Hits {
		if hit.Fields != nil {
			// Try to get chunk_text first, then fall back to other fields
			if chunkText, ok := hit.Fields["chunk_text"].(string); ok && chunkText != "" {
				matches = append(matches, chunkText)
			} else if category, ok := hit.Fields["category"].(string); ok && category != "" {
				matches = append(matches, category)
			}
		}
	}

	return matches, nil
}

func UpsertToPinecone(ctx context.Context, index *pinecone.IndexConnection, vectorID string, text string, metadata map[string]interface{}) error {
	// Use integrated embeddings - just upsert the text directly
	// Pinecone will automatically convert it to vectors using the hosted embedding model

	// Create the record with text field (should match your index's field_map configuration)
	record := pinecone.IntegratedRecord{
		"_id":        vectorID,
		"chunk_text": text,
		"category":   fmt.Sprintf("%v", metadata),
	}

	records := []*pinecone.IntegratedRecord{&record}

	err := index.UpsertRecords(ctx, records)
	if err != nil {
		return fmt.Errorf("failed to upsert text record to Pinecone: %w", err)
	}

	return nil
}
