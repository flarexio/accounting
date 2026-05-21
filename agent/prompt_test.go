package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/stoa/llm"
)

func TestPromptRenderer_IncludesActiveAccountsAndOpenPeriods(t *testing.T) {
	_, repo := awsBillScenario(t)
	renderer, err := agent.NewPromptRenderer(context.Background(), repo)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	messages, err := renderer.Render(llm.ReasoningInput{
		Task: "Paid AWS bill 100 USD using company credit card",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected exactly system + user with no events, got %d messages", len(messages))
	}
	if messages[0].Role != llm.MessageRoleSystem {
		t.Errorf("first message should be system, got %q", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "bookkeeping reasoning engine") {
		t.Errorf("system prompt missing identity line, got %q", messages[0].Content)
	}

	user := messages[1].Content
	for _, want := range []string{
		"Paid AWS bill 100 USD using company credit card",
		"Acme Cloud Co.",
		"5200 Cloud Hosting",
		"2100 Credit Card",
		"2026-05",
		"hq",
		"eu",
		`"side":"debit"`,
		"minor currency units",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt missing %q\n--- prompt ---\n%s", want, user)
		}
	}

	for _, hide := range []string{
		"5900 Legacy Office Rent",
		"2026-04",
	} {
		if strings.Contains(user, hide) {
			t.Errorf("user prompt should not include %q\n--- prompt ---\n%s", hide, user)
		}
	}
}

func TestPromptRenderer_AppendsValidationFeedbackAsMessages(t *testing.T) {
	_, repo := awsBillScenario(t)
	renderer, err := agent.NewPromptRenderer(context.Background(), repo)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	messages, err := renderer.Render(llm.ReasoningInput{
		Task: "Paid AWS bill",
		Events: []llm.CycleEvent{
			{Role: llm.EventRoleAssistant, Kind: llm.EventModelOutput, Content: "first attempt"},
			{Role: llm.EventRoleEnvironment, Kind: llm.EventValidationError, Content: "debits != credits"},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("expected system + user + 2 event messages, got %d", len(messages))
	}
	if messages[2].Role != llm.MessageRoleAssistant {
		t.Errorf("assistant event should map to assistant role, got %q", messages[2].Role)
	}
	if messages[3].Role != llm.MessageRoleUser {
		t.Errorf("environment event should map to user role, got %q", messages[3].Role)
	}
	if !strings.Contains(messages[3].Content, "debits != credits") {
		t.Errorf("validation_error event body missing from feedback message: %q", messages[3].Content)
	}
}

func TestNewPromptRenderer_NilRepo(t *testing.T) {
	if _, err := agent.NewPromptRenderer(context.Background(), nil); err == nil {
		t.Fatal("expected error when constructing renderer with nil repository")
	}
}
