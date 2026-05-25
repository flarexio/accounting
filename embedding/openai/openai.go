// Package openai provides an accounting.Embedder backed by the OpenAI
// Embeddings API. Persistence adapters and the benchmark runner consume it
// through the accounting.Embedder interface; they do not import this
// package's SDK types directly.
package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"

	"github.com/flarexio/accounting"
)

type embedder struct {
	client     openai.Client
	model      openai.EmbeddingModel
	dimensions int64
}

// NewEmbedder builds an accounting.Embedder backed by the OpenAI Embeddings API. dimensions must match downstream vector storage when one is in use.
func NewEmbedder(model string, dimensions int) accounting.Embedder {
	return &embedder{
		client:     openai.NewClient(),
		model:      model,
		dimensions: int64(dimensions),
	}
}

func (e *embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("openai embedder: empty input")
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
		return nil, fmt.Errorf("openai embedder: embed %q: %w", text, err)
	}
	if len(resp.Data) == 0 {
		return nil, errors.New("openai embedder: empty response")
	}
	src := resp.Data[0].Embedding
	out := make([]float32, len(src))
	for i, v := range src {
		out[i] = float32(v)
	}
	return out, nil
}
