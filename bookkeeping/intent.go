package bookkeeping

import (
	"encoding/json"
	"fmt"

	"github.com/flarexio/accounting"
)

// IntentKind tags which bookkeeping use case an Intent selects.
type IntentKind string

const (
	IntentPostJournal    IntentKind = "post_journal"
	IntentReverseJournal IntentKind = "reverse_journal"
	IntentSettle         IntentKind = "settle"
	IntentReject         IntentKind = "reject"
)

// Intent is the discriminated union the agent's model emits. Kind names the
// use case to run; the payload field matching Kind carries its typed
// arguments, and any others are ignored. Final marks the request's last action
// so the multi-action loop stops after executing it.
type Intent struct {
	Kind    IntentKind                `json:"kind"`
	Post    *accounting.JournalIntent `json:"post_journal,omitempty"`
	Reverse *ReverseIntent            `json:"reverse_journal,omitempty"`
	Settle  *SettleIntent             `json:"settle,omitempty"`
	Reject  *RejectIntent             `json:"reject,omitempty"`
	Final   bool                      `json:"final"`
}

// IsFinal reports whether this is the request's last action; the loop stops after executing it.
func (i Intent) IsFinal() bool { return i.Final }

// RejectIntent is the payload of a reject Intent.
type RejectIntent struct {
	Reason string `json:"reason"`
}

// ReverseIntent is the payload of a reverse_journal Intent. Reason is the
// classification stored on the resulting JournalRelation; Note is free-text
// rationale appended to the reversing entry's description.
type ReverseIntent struct {
	EntryID string                    `json:"entry_id"`
	Reason  accounting.RelationReason `json:"reason"`
	Note    string                    `json:"note,omitempty"`
}

// SettleIntent is the payload of a settle Intent: it posts a payment entry
// (Entry, typically Dr cash/bank, Cr the receivable/payable) and links it to
// the invoice/bill it clears with a settles JournalRelation. InvoiceEntryID is
// that invoice/bill's JE-id; Note is free-text rationale.
type SettleIntent struct {
	Entry          accounting.JournalIntent `json:"entry"`
	InvoiceEntryID string                   `json:"invoice_entry_id"`
	Note           string                   `json:"note,omitempty"`
}

// IntentDescriptor is the prompt-facing description of one Intent variant, so
// the prompt and the Registry never drift.
type IntentDescriptor struct {
	Kind      IntentKind
	Summary   string
	ArgsShape string // JSON skeleton of the payload object
}

const (
	postJournalArgsShape = `{"date":"2026-05-12","period_id":"<period_id>","currency":"USD","description":"...","source":null,"lines":[{"account_code":"<code>","side":"debit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>","counterparty_id":"<CP-id or empty>"}},{"account_code":"<code>","side":"credit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>","counterparty_id":""}}]}`

	reverseJournalArgsShape = `{"entry_id":"<JE-id of the posted entry to reverse>","reason":"<amount_error|account_error|duplicate|customer_cancel|period_end|other>","note":"..."}`

	settleArgsShape = `{"entry":{"date":"2026-05-20","period_id":"<period_id>","currency":"USD","description":"...","source":{"kind":"receipt","number":"..."},"lines":[{"account_code":"<cash/bank>","side":"debit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>","counterparty_id":""}},{"account_code":"<receivable/payable>","side":"credit","amount":10000,"memo":"...","dimensions":{"branch_id":"<branch_id>","counterparty_id":"<CP-id>"}}]},"invoice_entry_id":"<JE-id of the invoice this clears>","note":"..."}`

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
			Kind:      IntentSettle,
			Summary:   "Record a payment that settles an invoice/bill: post the receipt entry and link it to the invoice's JE-id.",
			ArgsShape: settleArgsShape,
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

// postPayloadSchema is the strict object schema for a journal posting, shared by
// post_journal and settle.entry (interpolated into intentSchemaJSON) so the two
// never drift.
const postPayloadSchema = `{
          "type": "object",
          "additionalProperties": false,
          "required": ["date", "period_id", "currency", "description", "lines", "source"],
          "properties": {
            "date": { "type": "string", "description": "Business date in the company's timezone, format YYYY-MM-DD (e.g. 2026-05-12), inside the chosen period" },
            "period_id": { "type": "string" },
            "currency": { "type": "string", "description": "ISO 4217 code (USD, TWD, ...)" },
            "description": { "type": "string" },
            "source": {
              "anyOf": [
                { "type": "null" },
                {
                  "type": "object",
                  "additionalProperties": false,
                  "required": ["kind", "number"],
                  "properties": {
                    "kind": { "type": "string", "enum": ["invoice", "bill", "receipt"], "description": "invoice = a sales invoice you issued; bill = a purchase invoice a supplier issued; receipt = a payment receipt." },
                    "number": { "type": "string", "description": "The document number (e.g. 統一發票 AB-12345678), or empty string if none." }
                  }
                }
              ],
              "description": "The invoice/receipt this entry records, or null for entries with no source document."
            },
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
                    "required": ["branch_id", "counterparty_id"],
                    "properties": {
                      "branch_id": { "type": "string", "description": "Required reporting branch this line is posted to; must come from the periods/branches list. All lines on one entry share the same branch_id." },
                      "counterparty_id": { "type": "string", "description": "Customer/supplier this line is attributed to, from find_counterparties (e.g. CP-0001); empty string for cash/tax/internal lines with no counterparty. All lines that set it on one entry must share the same value. Set it on the receivable/payable line of an AR/AP transaction." }
                    }
                  }
                }
              }
            }
          }
        }`

var intentSchemaJSON = fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "post_journal", "reverse_journal", "settle", "reject", "final"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["post_journal", "reverse_journal", "settle", "reject"]
    },
    "final": {
      "type": "boolean",
      "description": "true if this action completes the request; false if you will follow it with another action this turn-cycle. A single post/reverse/settle/reject is itself final. The loop stops once it executes a final action."
    },
    "post_journal": {
      "anyOf": [
        { "type": "null" },
        %[1]s
      ]
    },
    "settle": {
      "anyOf": [
        { "type": "null" },
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["entry", "invoice_entry_id", "note"],
          "properties": {
            "entry": %[1]s,
            "invoice_entry_id": { "type": "string", "description": "JE-id of the invoice or bill entry this payment settles (the receivable/payable being cleared)." },
            "note": { "type": "string", "description": "Optional free-text note on the settlement, e.g. 'paid in full' or 'partial payment'. May be empty." }
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
          "required": ["entry_id", "reason", "note"],
          "properties": {
            "entry_id": { "type": "string", "description": "JE-id of the posted entry to reverse." },
            "reason": {
              "type": "string",
              "enum": ["amount_error", "account_error", "duplicate", "customer_cancel", "period_end", "other"],
              "description": "Classification of why the entry is being reversed. Pick the most specific code: amount_error (wrong numbers), account_error (wrong account chosen), duplicate (same transaction posted twice), customer_cancel (refund/cancellation), period_end (closing adjustment). Use 'other' only when none of the above fits."
            },
            "note": { "type": "string", "description": "State the factual error in one short sentence, in the same language as the original entry's description -- e.g. 'amount should be 95000, not 105000', 'duplicate of JE-0050', 'customer cancelled on 5/15'. Do not restate that a reversal is happening; that is implied. May be empty." }
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
}`, postPayloadSchema)
