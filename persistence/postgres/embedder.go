package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
)

// Embedder turns text into a fixed-dimension vector for pgvector storage and similarity search.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type openAIEmbedder struct {
	client     openai.Client
	model      openai.EmbeddingModel
	dimensions int64
}

// NewOpenAIEmbedder builds an Embedder backed by the OpenAI Embeddings API. dimensions must match the schema's vector column width.
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
