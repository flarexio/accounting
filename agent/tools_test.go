package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

func TestAgent_RunsToolCallBeforePosting(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	var calls int
	engine := fakeEngineFunc(func(_ context.Context, input llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		calls++
		if calls == 1 {
			specs := input.Tools
			var sawSpec bool
			for _, s := range specs {
				if s.Name == "find_accounts" && len(s.ArgsSchema) > 0 {
					sawSpec = true
				}
			}
			if !sawSpec {
				t.Errorf("first turn should receive find_accounts ToolSpec with an ArgsSchema, got %+v", specs)
			}
			return llm.ToolCallsOutput[bookkeeping.Intent](
				[]llm.ToolCall{{
					Name: "find_accounts",
					Args: json.RawMessage(`{"query":"company credit card payable","type":""}`),
				}},
				nil,
				"look up the credit-card account first",
			), nil
		}
		var sawTool bool
		for _, ev := range input.Events {
			if ev.Kind == llm.EventToolResult && strings.Contains(ev.Content, "2100") {
				sawTool = true
			}
		}
		if !sawTool {
			t.Error("second turn did not receive the find_accounts result in its events")
		}
		return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, "post the balanced entry"), nil
	})

	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}
	res, err := agent.Book(context.Background(), "Paid the AWS bill on the company credit card")
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if calls != 2 {
		t.Fatalf("engine called %d times, want 2 (one tool round, one post)", calls)
	}
	if res.Entry.ID == "" {
		t.Error("expected an entry to be posted after the tool round")
	}
	var sawToolEvent bool
	for _, ev := range res.Events {
		if ev.Kind == llm.EventToolResult {
			sawToolEvent = true
		}
	}
	if !sawToolEvent {
		t.Error("result events should include the find_accounts tool result")
	}
}

func TestPromptRenderer_SwitchesToToolModeForLargeChart(t *testing.T) {
	mk := func(n int) []accounting.Account {
		accounts := make([]accounting.Account, n)
		for i := range accounts {
			accounts[i] = accounting.Account{
				Code:   fmt.Sprintf("%04d", 1000+i),
				Name:   fmt.Sprintf("Account %d", i),
				Type:   accounting.AccountAsset,
				Active: true,
			}
		}
		return accounts
	}

	small := agent.PromptRenderer{Accounts: mk(3)}
	msgs, err := small.Render(llm.ReasoningInput{Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[0].Content, "Active chart of accounts") {
		t.Error("a small chart should list the accounts in the system prompt")
	}
	if !strings.Contains(msgs[0].Content, "Account 1 ") {
		t.Error("a small chart should enumerate accounts in the system prompt")
	}

	large := agent.PromptRenderer{Accounts: mk(20)}
	msgs, err = large.Render(llm.ReasoningInput{Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[0].Content, "not listed -- use find_accounts") {
		t.Error("a large chart should be summarized and direct the model to find_accounts")
	}
	if strings.Contains(msgs[0].Content, "Account 17") {
		t.Error("a large chart must not be dumped account-by-account")
	}
}
