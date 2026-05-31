// Package openai provides an accounting.AccountReranker backed by the OpenAI
// chat completions API. It reorders hybrid-retrieval candidates by relevance
// before the bookkeeper agent sees them; callers consume it through the
// accounting.AccountReranker interface, not this package's SDK types.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"github.com/flarexio/accounting"
)

type reranker struct {
	client openai.Client
	model  string
}

// NewReranker builds an accounting.AccountReranker backed by the OpenAI chat
// completions API using model.
func NewReranker(model string) accounting.AccountReranker {
	return &reranker{client: openai.NewClient(), model: model}
}

const rerankSystemPrompt = "You rank chart-of-accounts candidates by how well each fits the described transaction. " +
	"Return a JSON object {\"codes\": [...]} listing every candidate's code exactly once, most relevant first. " +
	"Use only codes from the candidate list; do not invent or drop any."

func (r *reranker) Rerank(ctx context.Context, query string, candidates []accounting.Account, limit int) ([]accounting.Account, error) {
	if len(candidates) <= 1 {
		return capAccounts(candidates, limit), nil
	}
	resp, err := r.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       r.model,
		Temperature: param.NewOpt(0.0),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(rerankSystemPrompt),
			openai.UserMessage(rerankUserPrompt(query, candidates)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai reranker: rerank %q: %w", query, err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("openai reranker: empty response")
	}
	ranked, err := applyRanking(resp.Choices[0].Message.Content, candidates)
	if err != nil {
		return nil, err
	}
	return capAccounts(ranked, limit), nil
}

// rerankUserPrompt lists the query and the candidates as "code\tname (type)" lines.
func rerankUserPrompt(query string, candidates []accounting.Account) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Transaction: %s\n\nCandidates:", query)
	for _, a := range candidates {
		fmt.Fprintf(&b, "\n%s\t%s (%s)", a.Code, a.Name, a.Type)
	}
	return b.String()
}

// applyRanking reorders candidates to follow the model's code list, ignoring
// unknown or duplicate codes and appending any candidate the model omitted in
// its original position, so no candidate is ever dropped. A response with no
// usable "codes" key leaves the original order intact.
func applyRanking(content string, candidates []accounting.Account) ([]accounting.Account, error) {
	var parsed struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("openai reranker: decode ranking: %w", err)
	}
	byCode := make(map[string]accounting.Account, len(candidates))
	for _, a := range candidates {
		byCode[a.Code] = a
	}
	seen := make(map[string]bool, len(candidates))
	out := make([]accounting.Account, 0, len(candidates))
	for _, code := range parsed.Codes {
		if a, ok := byCode[code]; ok && !seen[code] {
			out = append(out, a)
			seen[code] = true
		}
	}
	for _, a := range candidates {
		if !seen[a.Code] {
			out = append(out, a)
		}
	}
	return out, nil
}

func capAccounts(accounts []accounting.Account, limit int) []accounting.Account {
	if limit > 0 && len(accounts) > limit {
		return accounts[:limit]
	}
	return accounts
}
