package nats

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/flarexio/accounting"
)

func sampleEvent() accounting.JournalPosted {
	return accounting.JournalPosted{
		Entry: accounting.JournalEntry{
			Date:        accounting.NewDate(2026, 5, 12),
			PeriodID:    "2026-05",
			Currency:    "USD",
			Description: "Paid AWS bill",
			Lines: []accounting.JournalLine{
				{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000, Memo: "Cloud", Dimensions: accounting.Dimensions{BranchID: "hq"}},
				{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000, Memo: "Card", Dimensions: accounting.Dimensions{BranchID: "hq"}},
			},
			PostedAt: time.Date(2026, 5, 12, 9, 0, 1, 0, time.UTC),
		},
	}
}

func TestEncodeEvent_OmitsSubjectAndSequence(t *testing.T) {
	evt := sampleEvent()
	evt.Subject = "accounting.journal"
	evt.Sequence = 42
	evt.Entry.ID = "JE-0042"

	body, err := encodeAccountingEvent(evt)
	if err != nil {
		t.Fatalf("encodeAccountingEvent: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if _, present := raw["Subject"]; present {
		t.Errorf("Subject should not be on the wire, body=%s", string(body))
	}
	if _, present := raw["Sequence"]; present {
		t.Errorf("Sequence should not be on the wire, body=%s", string(body))
	}
	if _, present := raw["entry"]; !present {
		t.Errorf("entry should be on the wire, body=%s", string(body))
	}
}

func TestDecodeEvent_StampsSubjectSequenceAndCarriesEntryID(t *testing.T) {
	in := sampleEvent()
	in.Entry.ID = accounting.FormatEntryID(7)

	body, err := encodeAccountingEvent(in)
	if err != nil {
		t.Fatalf("encodeAccountingEvent: %v", err)
	}
	got, err := decodeAccountingEvent(body, "accounting.journal", 7)
	if err != nil {
		t.Fatalf("decodeAccountingEvent: %v", err)
	}
	if got.Subject != "accounting.journal" {
		t.Errorf("subject: got %q", got.Subject)
	}
	if got.Sequence != 7 {
		t.Errorf("sequence: got %d", got.Sequence)
	}
	if got.Entry.ID != in.Entry.ID {
		t.Errorf("entry id: want %q got %q", in.Entry.ID, got.Entry.ID)
	}
	if got.Entry.PeriodID != "2026-05" {
		t.Errorf("period id round-trip lost: %+v", got.Entry)
	}
	if len(got.Entry.Lines) != 2 {
		t.Errorf("lines round-trip lost: %+v", got.Entry.Lines)
	}
}

func TestStampPubAck_StampsSubjectSequenceWithoutTouchingEntryID(t *testing.T) {
	in := sampleEvent()
	in.Entry.ID = accounting.FormatEntryID(99)

	stamped := stampAccountingPubAck(in, "accounting.journal", 99)
	if stamped.Subject != "accounting.journal" || stamped.Sequence != 99 {
		t.Errorf("metadata not stamped: %+v", stamped)
	}
	if stamped.Entry.ID != in.Entry.ID {
		t.Errorf("Entry.ID: want %q (producer-assigned) got %q", in.Entry.ID, stamped.Entry.ID)
	}
}

func TestIsWrongLastSequence(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"WrongLastSequence APIError", &jetstream.APIError{Code: 400, ErrorCode: jetstream.JSErrCodeStreamWrongLastSequence}, true},
		{"wrapped APIError", fmt.Errorf("publish: %w", &jetstream.APIError{Code: 400, ErrorCode: jetstream.JSErrCodeStreamWrongLastSequence}), true},
		{"other APIError", &jetstream.APIError{Code: 503, ErrorCode: jetstream.ErrorCode(10009)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWrongLastSequence(tc.err); got != tc.want {
				t.Errorf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestEncodeDecode_RoundTripPreservesTags(t *testing.T) {
	evt := sampleEvent()
	evt.Entry.Lines[0].Dimensions.Tags = map[string]string{"project": "atlas"}

	body, err := encodeAccountingEvent(evt)
	if err != nil {
		t.Fatalf("encodeAccountingEvent: %v", err)
	}
	got, err := decodeAccountingEvent(body, "accounting.journal", 1)
	if err != nil {
		t.Fatalf("decodeAccountingEvent: %v", err)
	}
	if got.Entry.Lines[0].Dimensions.Tags["project"] != "atlas" {
		t.Errorf("tag round-trip lost: %+v", got.Entry.Lines[0].Dimensions.Tags)
	}
}
