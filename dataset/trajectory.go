// Package dataset captures a teacher model's completed bookkeeping runs as an
// append-only JSONL corpus for later distillation into a smaller student model.
// Each line is one validated trajectory: the request, the full reason/tool/
// observe loop, and the final Intent the teacher committed.
package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

// SchemaVersion tags the on-disk record shape; bump it on any breaking change to Record.
const SchemaVersion = "1"

// Provenance records which teacher and prompt contract produced a trajectory, so
// records from a changed model or prompt are never silently mixed at train time.
type Provenance struct {
	TeacherModel  string `json:"teacher_model"`
	PromptVersion string `json:"prompt_version"`
}

// Record is one training example: a teacher's full trajectory for a single
// request, kept only when the run validated and committed (or cleanly rejected).
type Record struct {
	SchemaVersion string             `json:"schema_version"`
	RecordedAt    time.Time          `json:"recorded_at"`
	Provenance    Provenance         `json:"provenance"`
	Request       string             `json:"request"`
	Trajectory    []llm.CycleEvent   `json:"trajectory"`
	Intent        bookkeeping.Intent `json:"intent"`
	EntryIDs      []string           `json:"entry_ids"`
	Turns         int                `json:"turns"`
}

// FromResult builds a Record from a completed Bookkeeper run; call it only when Book returned no error.
func FromResult(request string, res agent.Result, prov Provenance, now time.Time) Record {
	ids := make([]string, len(res.Entries))
	for i, e := range res.Entries {
		ids[i] = e.ID
	}
	return Record{
		SchemaVersion: SchemaVersion,
		RecordedAt:    now.UTC(),
		Provenance:    prov,
		Request:       request,
		Trajectory:    res.Events,
		Intent:        res.Intent,
		EntryIDs:      ids,
		Turns:         res.Turns,
	}
}

// Recorder appends Records as JSONL to a file, one record per line.
type Recorder struct {
	mu sync.Mutex
	f  *os.File
}

// NewFileRecorder opens path for appending, creating it if absent.
func NewFileRecorder(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dataset: open %s: %w", path, err)
	}
	return &Recorder{f: f}, nil
}

// Record marshals rec as one JSON line and appends it; a nil Recorder is a no-op.
func (r *Recorder) Record(rec Record) error {
	if r == nil {
		return nil
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("dataset: marshal record: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("dataset: append record: %w", err)
	}
	return nil
}

// Close closes the underlying file; a nil Recorder is a no-op.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	return r.f.Close()
}
