package nats

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// TestCodec_CoversEverySupportedSubject guards that encodeEvent and
// decodeBySubject handle every subject the bus advertises -- a new event type
// added to supportedSubjects but not the codec fails here instead of only at
// runtime on NATS (the in-proc bus does not encode).
func TestCodec_CoversEverySupportedSubject(t *testing.T) {
	period := accounting.Period{ID: "2026-05", Start: accounting.NewDate(2026, 5, 1), End: accounting.NewDate(2026, 5, 31), Status: accounting.PeriodOpen}
	samples := map[string]bookkeeping.Event{
		accounting.SubjectJournalPosted:     sampleEvent(),
		accounting.SubjectPeriodClosure:     accounting.PeriodClosure{Period: period},
		accounting.SubjectCompanyConfigured: accounting.CompanyConfigured{Company: accounting.Company{ID: "acme"}},
		accounting.SubjectAccountAdded:      accounting.AccountAdded{Account: accounting.Account{Code: "1000"}},
		accounting.SubjectBranchAdded:       accounting.BranchAdded{Branch: accounting.Branch{ID: "main"}},
		accounting.SubjectPeriodAdded:       accounting.PeriodAdded{Period: period},
		accounting.SubjectCounterpartyAdded: accounting.CounterpartyAdded{Counterparty: accounting.Counterparty{ID: "CP-0001"}},
		accounting.SubjectPolicySet:         accounting.PolicySet{Policy: "x"},
	}
	for _, subject := range supportedSubjects {
		evt, ok := samples[subject]
		if !ok {
			t.Fatalf("no sample event for supported subject %q; add one and make sure encodeEvent/decodeBySubject handle it", subject)
		}
		if evt.EventSubject() != subject {
			t.Fatalf("sample for %q has EventSubject %q", subject, evt.EventSubject())
		}
		body, err := encodeEvent(evt)
		if err != nil {
			t.Fatalf("encodeEvent for subject %q: %v", subject, err)
		}
		decoded, err := decodeBySubject(subject, body)
		if err != nil {
			t.Fatalf("decodeBySubject for subject %q: %v", subject, err)
		}
		if decoded.EventSubject() != subject {
			t.Fatalf("decoded event for %q has EventSubject %q", subject, decoded.EventSubject())
		}
	}
}

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

func TestEncodeEvent_BodyIsJustThePayload(t *testing.T) {
	evt := sampleEvent()
	evt.Entry.ID = "JE-0042"

	body, err := encodeEvent(evt)
	if err != nil {
		t.Fatalf("encodeEvent: %v", err)
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

func TestDecodeBody_RoundTripsAndCarriesEntryID(t *testing.T) {
	in := sampleEvent()
	in.Entry.ID = accounting.FormatEntryID(7)

	body, err := encodeEvent(in)
	if err != nil {
		t.Fatalf("encodeEvent: %v", err)
	}
	evt, err := decodeBody[accounting.JournalPosted](body)
	if err != nil {
		t.Fatalf("decodeBody: %v", err)
	}
	got := evt.(accounting.JournalPosted)
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

	body, err := encodeEvent(evt)
	if err != nil {
		t.Fatalf("encodeEvent: %v", err)
	}
	decoded, err := decodeBody[accounting.JournalPosted](body)
	if err != nil {
		t.Fatalf("decodeBody: %v", err)
	}
	got := decoded.(accounting.JournalPosted)
	if got.Entry.Lines[0].Dimensions.Tags["project"] != "atlas" {
		t.Errorf("tag round-trip lost: %+v", got.Entry.Lines[0].Dimensions.Tags)
	}
}
