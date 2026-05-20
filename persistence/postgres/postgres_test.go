package postgres

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/postgres/pgstore"
)

// Pure pgstore<->accounting mapping helpers; real Postgres integration is run manually.

func TestAccountFromRow(t *testing.T) {
	got := accountFromRow(pgstore.Account{
		Code:   "5200",
		Name:   "Cloud Hosting",
		Type:   "expense",
		Active: true,
	})
	want := accounting.Account{
		Code:   "5200",
		Name:   "Cloud Hosting",
		Type:   accounting.AccountExpense,
		Active: true,
	}
	if got != want {
		t.Errorf("account mismatch:\nwant %+v\n got %+v", want, got)
	}
}

func TestPeriodFromRow_PreservesTimestamps(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got := periodFromRow(pgstore.Period{
		ID:      "2026-05",
		StartAt: pgtype.Timestamptz{Time: start, Valid: true},
		EndAt:   pgtype.Timestamptz{Time: end, Valid: true},
		Status:  "open",
	})
	want := accounting.Period{
		ID:     "2026-05",
		Start:  start,
		End:    end,
		Status: accounting.PeriodOpen,
	}
	if !got.Start.Equal(want.Start) || !got.End.Equal(want.End) {
		t.Errorf("period timestamps mismatch:\nwant %+v\n got %+v", want, got)
	}
	if got.ID != want.ID || got.Status != want.Status {
		t.Errorf("period scalar mismatch:\nwant %+v\n got %+v", want, got)
	}
}

func TestLinesFromRows_NilWhenEmpty(t *testing.T) {
	lines, err := linesFromRows(nil)
	if err != nil {
		t.Fatalf("linesFromRows: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil slice for empty rows, got %v", lines)
	}
}

func TestLinesFromRows_DecodesTags(t *testing.T) {
	tags := map[string]string{"project": "atlas", "team": "platform"}
	raw, _ := json.Marshal(tags)

	rows := []pgstore.JournalLine{{
		EntryID:     "JE-0001",
		LineNo:      0,
		AccountCode: "5200",
		Side:        "debit",
		Amount:      10000,
		Memo:        "AWS",
		BranchID:    "hq",
		Tags:        raw,
	}}
	lines, err := linesFromRows(rows)
	if err != nil {
		t.Fatalf("linesFromRows: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	got := lines[0]
	if got.AccountCode != "5200" || got.Side != accounting.SideDebit || got.Amount != 10000 {
		t.Errorf("scalar mismatch: %+v", got)
	}
	if got.Dimensions.BranchID != "hq" {
		t.Errorf("branch id mismatch: %q", got.Dimensions.BranchID)
	}
	if !reflect.DeepEqual(got.Dimensions.Tags, tags) {
		t.Errorf("tags mismatch: want %v got %v", tags, got.Dimensions.Tags)
	}
}

func TestMarshalUnmarshalTags_RoundTrip(t *testing.T) {
	tags := map[string]string{"env": "prod"}
	raw, err := marshalAccountingTags(tags)
	if err != nil {
		t.Fatalf("marshalAccountingTags: %v", err)
	}
	got, err := unmarshalAccountingTags(raw)
	if err != nil {
		t.Fatalf("unmarshalAccountingTags: %v", err)
	}
	if !reflect.DeepEqual(got, tags) {
		t.Errorf("tags round-trip mismatch: want %v got %v", tags, got)
	}
}

func TestMarshalTags_EmptyMapBecomesNil(t *testing.T) {
	raw, err := marshalAccountingTags(nil)
	if err != nil {
		t.Fatalf("marshalAccountingTags(nil): %v", err)
	}
	if raw != nil {
		t.Errorf("nil map should marshal to nil bytes, got %q", string(raw))
	}
	raw, err = marshalAccountingTags(map[string]string{})
	if err != nil {
		t.Fatalf("marshalAccountingTags(empty): %v", err)
	}
	if raw != nil {
		t.Errorf("empty map should marshal to nil bytes, got %q", string(raw))
	}
}

func TestUnmarshalTags_EmptyInputs(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("{}")} {
		got, err := unmarshalAccountingTags(in)
		if err != nil {
			t.Fatalf("unmarshalAccountingTags(%q): %v", string(in), err)
		}
		if got != nil {
			t.Errorf("unmarshalAccountingTags(%q) want nil, got %v", string(in), got)
		}
	}
}
