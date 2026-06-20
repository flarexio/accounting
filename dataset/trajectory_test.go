package dataset_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/dataset"
	"github.com/flarexio/stoa/llm"
)

func sampleResult() agent.Result {
	return agent.Result{
		SystemPrompt: "Company: Demo\nActive chart of accounts:\n...",
		Intent:       bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal, Final: true},
		Entries: []accounting.JournalEntry{
			{ID: "JE-0001"},
			{ID: "JE-0002"},
		},
		Turns: 3,
		Events: []llm.CycleEvent{
			{Role: llm.EventRoleUser, Kind: llm.EventTask, Content: "buy coffee"},
			{Role: llm.EventRoleAssistant, Kind: llm.EventModelOutput, Content: "{...}"},
		},
	}
}

func TestFromResult(t *testing.T) {
	now := time.Date(2026, 6, 20, 8, 30, 0, 0, time.FixedZone("CST", 8*3600))
	prov := dataset.Provenance{TeacherModel: "gpt-5.5", PromptVersion: "v1"}

	rec := dataset.FromResult("buy coffee", sampleResult(), prov, now)

	if rec.SchemaVersion != dataset.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", rec.SchemaVersion, dataset.SchemaVersion)
	}
	if !rec.RecordedAt.Equal(now) || rec.RecordedAt.Location() != time.UTC {
		t.Errorf("RecordedAt = %v, want UTC instant equal to %v", rec.RecordedAt, now)
	}
	if rec.Provenance != prov {
		t.Errorf("Provenance = %+v, want %+v", rec.Provenance, prov)
	}
	if want := []string{"JE-0001", "JE-0002"}; len(rec.EntryIDs) != 2 || rec.EntryIDs[0] != want[0] || rec.EntryIDs[1] != want[1] {
		t.Errorf("EntryIDs = %v, want %v", rec.EntryIDs, want)
	}
	if rec.Turns != 3 {
		t.Errorf("Turns = %d, want 3", rec.Turns)
	}
	if len(rec.Trajectory) != 2 {
		t.Errorf("Trajectory length = %d, want 2", len(rec.Trajectory))
	}
	if rec.SystemPrompt != "Company: Demo\nActive chart of accounts:\n..." {
		t.Errorf("SystemPrompt = %q, want the captured system message", rec.SystemPrompt)
	}
}

func TestRecorderAppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	prov := dataset.Provenance{TeacherModel: "gpt-5.5", PromptVersion: "v1"}

	rec := dataset.FromResult("buy coffee", sampleResult(), prov, time.Now())

	// Two separate recorders writing the same path must accumulate, not truncate.
	for range 2 {
		r, err := dataset.NewFileRecorder(path)
		if err != nil {
			t.Fatalf("NewFileRecorder: %v", err)
		}
		if err := r.Record(rec); err != nil {
			t.Fatalf("Record: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var lines int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines++
		var got dataset.Record
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", lines, err)
		}
		if got.Request != "buy coffee" {
			t.Errorf("line %d Request = %q, want %q", lines, got.Request, "buy coffee")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != 2 {
		t.Errorf("got %d lines, want 2", lines)
	}
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *dataset.Recorder
	if err := r.Record(dataset.Record{}); err != nil {
		t.Errorf("Record on nil = %v, want nil", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close on nil = %v, want nil", err)
	}
}
