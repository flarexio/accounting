package bookkeeping

import "github.com/flarexio/accounting"

// IntentKind tags which bookkeeping use case an Intent selects.
type IntentKind string

const (
	IntentPostJournal    IntentKind = "post_journal"
	IntentReverseJournal IntentKind = "reverse_journal"
)

// Intent is the discriminated union the agent's model emits. Kind names the
// use case to run; the payload field matching Kind carries its typed
// arguments, and any others are ignored.
type Intent struct {
	Kind    IntentKind                `json:"kind"`
	Post    *accounting.JournalIntent `json:"post_journal,omitempty"`
	Reverse *ReverseIntent            `json:"reverse_journal,omitempty"`
}

// ReverseIntent is the payload of a reverse_journal Intent.
type ReverseIntent struct {
	EntryID string `json:"entry_id"`
	Reason  string `json:"reason,omitempty"`
}

// IntentDescriptor is the prompt-facing description of one Intent variant, so
// the prompt and the Registry never drift.
type IntentDescriptor struct {
	Kind      IntentKind
	Summary   string
	ArgsShape string // JSON skeleton of the payload object
}

const (
	postJournalArgsShape = `{"date":"2026-05-12T00:00:00Z","period_id":"<period_id>","currency":"USD","description":"...","lines":[{"account_code":"<code>","side":"debit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>"}},{"account_code":"<code>","side":"credit","amount":10000,"memo":"...","dimensions":{}}]}`

	reverseJournalArgsShape = `{"entry_id":"<JE-id of the posted entry to reverse>","reason":"..."}`
)

// Intents returns the descriptor for every IntentKind, ordered by Kind. It is
// the single source of the agent's vocabulary; NewBookkeepingRegistry routes
// exactly these kinds.
func Intents() []IntentDescriptor {
	return []IntentDescriptor{
		{
			Kind:      IntentPostJournal,
			Summary:   "Post a new balanced double-entry journal entry.",
			ArgsShape: postJournalArgsShape,
		},
		{
			Kind:      IntentReverseJournal,
			Summary:   "Reverse an existing posted entry, named by its JE-id, with a mirror-image entry.",
			ArgsShape: reverseJournalArgsShape,
		},
	}
}
