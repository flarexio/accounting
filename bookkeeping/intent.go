package bookkeeping

import (
	"encoding/json"

	"github.com/flarexio/accounting"
)

// IntentKind tags which bookkeeping use case an Intent selects.
type IntentKind string

const (
	IntentPostJournal    IntentKind = "post_journal"
	IntentReverseJournal IntentKind = "reverse_journal"
	IntentReject         IntentKind = "reject"
)

// Intent is the discriminated union the agent's model emits. Kind names the
// use case to run; the payload field matching Kind carries its typed
// arguments, and any others are ignored.
type Intent struct {
	Kind    IntentKind                `json:"kind"`
	Post    *accounting.JournalIntent `json:"post_journal,omitempty"`
	Reverse *ReverseIntent            `json:"reverse_journal,omitempty"`
	Reject  *RejectIntent             `json:"reject,omitempty"`
}

// RejectIntent is the payload of a reject Intent.
type RejectIntent struct {
	Reason string `json:"reason"`
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
	postJournalArgsShape = `{"date":"2026-05-12","period_id":"<period_id>","currency":"USD","description":"...","lines":[{"account_code":"<code>","side":"debit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>"}},{"account_code":"<code>","side":"credit","amount":10000,"memo":"...","dimensions":{}}]}`

	reverseJournalArgsShape = `{"entry_id":"<JE-id of the posted entry to reverse>","reason":"..."}`

	rejectArgsShape = `{"reason":"<explanation why the request cannot be fulfilled>"}`
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
		{
			Kind:      IntentReject,
			Summary:   "Decline a request that cannot be fulfilled; provide a reason.",
			ArgsShape: rejectArgsShape,
		},
	}
}

// IntentSchema returns the JSON Schema for the Intent discriminated union, in
// OpenAI structured-outputs strict form: every property is required, payloads
// for unused kinds are nullable, no additional properties are allowed. The
// schema is wrapped by the stoa adapter into the {evidence, rationale, intent}
// envelope.
func IntentSchema() json.RawMessage {
	return json.RawMessage(intentSchemaJSON)
}

const intentSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "post_journal", "reverse_journal", "reject"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["post_journal", "reverse_journal", "reject"]
    },
    "post_journal": {
      "anyOf": [
        { "type": "null" },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["date", "period_id", "currency", "description", "lines"],
          "properties": {
            "date": { "type": "string", "description": "Business date in the company's timezone, format YYYY-MM-DD (e.g. 2026-05-12), inside the chosen period" },
            "period_id": { "type": "string" },
            "currency": { "type": "string", "description": "ISO 4217 code (USD, TWD, ...)" },
            "description": { "type": "string" },
            "lines": {
              "type": "array",
              "description": "At least two lines; total debit must equal total credit.",
              "items": {
                "type": "object",
                "additionalProperties": false,
                "required": ["account_code", "side", "amount", "memo", "dimensions"],
                "properties": {
                  "account_code": { "type": "string" },
                  "side": { "type": "string", "enum": ["debit", "credit"] },
                  "amount": { "type": "integer", "description": "Minor currency units, per ISO 4217 exponent." },
                  "memo": { "type": "string" },
                  "dimensions": {
                    "type": "object",
                    "additionalProperties": false,
                    "required": ["branch_id"],
                    "properties": {
                      "branch_id": { "type": "string", "description": "Empty string when no branch dimension applies." }
                    }
                  }
                }
              }
            }
          }
        }
      ]
    },
    "reverse_journal": {
      "anyOf": [
        { "type": "null" },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["entry_id", "reason"],
          "properties": {
            "entry_id": { "type": "string", "description": "JE-id of the posted entry to reverse." },
            "reason": { "type": "string" }
          }
        }
      ]
    },
    "reject": {
      "anyOf": [
        { "type": "null" },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["reason"],
          "properties": {
            "reason": { "type": "string" }
          }
        }
      ]
    }
  }
}`
