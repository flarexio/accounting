package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
)

// Embedder turns an account-identifying string into a fixed-dimension vector.
// The postgres adapter uses it both at PutAccount (to write the vector column)
// and at FindAccounts (to embed the query before similarity search).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// openAIEmbedder calls the OpenAI Embeddings API. The OpenAI client reads
// OPENAI_API_KEY from the environment by default.
type openAIEmbedder struct {
	client     openai.Client
	model      openai.EmbeddingModel
	dimensions int64
}

// NewOpenAIEmbedder builds an Embedder backed by OpenAI's text-embedding-3-*
// family. dimensions must match the accounts.embedding column width set by
// migration 0002 (1536 by default); a mismatch surfaces as a pgvector error
// at the first PutAccount call.
func NewOpenAIEmbedder(model string, dimensions int) Embedder {
	return &openAIEmbedder{
		client:     openai.NewClient(),
		model:      model,
		dimensions: int64(dimensions),
	}
}

func (e *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("postgres: embedder: empty input")
	}
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfString: param.NewOpt(text)},
		Model: e.model,
	}
	if e.dimensions > 0 {
		params.Dimensions = param.NewOpt(e.dimensions)
	}
	resp, err := e.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("postgres: embed %q: %w", text, err)
	}
	if len(resp.Data) == 0 {
		return nil, errors.New("postgres: embed: empty response")
	}
	src := resp.Data[0].Embedding
	out := make([]float32, len(src))
	for i, v := range src {
		out[i] = float32(v)
	}
	return out, nil
}
