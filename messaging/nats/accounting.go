package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// streamSubjectAccounting is the JetStream stream's binding pattern: a wildcard
// over the accounting namespace, so future subjects need no stream reconfig.
const streamSubjectAccounting = "accounting.>"

// supportedSubjects names every event subject NewAccountingBus can publish
// and dispatch. The list drives the FilterSubjects on the durable consumer
// and gates Publish/dispatch against typos at startup.
var supportedSubjects = []string{
	accounting.SubjectJournalPosted,
	accounting.SubjectPeriodClosure,
	accounting.SubjectCompanyConfigured,
	accounting.SubjectAccountAdded,
	accounting.SubjectBranchAdded,
	accounting.SubjectPeriodAdded,
	accounting.SubjectPolicySet,
}

// accountingBus is the NATS JetStream backed bookkeeping.EventBus for the
// accounting domain. One durable consumer fans every accounting subject
// through a single ack cursor so the global publish order between subjects
// is preserved; the consume loop dispatches each message to the Router
// registered via Subscribe. Broker "wrong last sequence" rejections are
// translated to accounting.ErrConcurrentUpdate so the inproc and NATS
// transports surface the same sentinel.
type accountingBus struct {
	bus *bus

	mu      sync.Mutex
	started bool
	router  *bookkeeping.Router
}

// NewAccountingBus opens NATS and returns a bookkeeping.EventBus configured
// for every supported accounting subject. Close drains the consume loop and
// releases the connection.
func NewAccountingBus(ctx context.Context, cfg Config) (bookkeeping.EventBus, error) {
	b, err := connect(ctx, cfg, supportedSubjects, streamSubjectAccounting)
	if err != nil {
		return nil, err
	}
	return &accountingBus{bus: b}, nil
}

// Publish encodes evt and publishes it to the subject from evt.EventSubject().
// The optimistic-concurrency hint is forwarded as
// Nats-Expected-Last-Subject-Sequence; a stale hint surfaces as
// accounting.ErrConcurrentUpdate.
func (a *accountingBus) Publish(ctx context.Context, evt bookkeeping.Event, expect accounting.ExpectedSequence) error {
	subject := evt.EventSubject()
	body, err := encodeEvent(evt)
	if err != nil {
		return err
	}
	opts := []jetstream.PublishOpt{}
	if expect.Subject != "" {
		opts = append(opts, jetstream.WithExpectLastSequencePerSubject(expect.LastSeq))
	}
	if _, err := a.bus.publishRaw(ctx, subject, body, opts...); err != nil {
		if isWrongLastSequence(err) {
			return accounting.ErrConcurrentUpdate
		}
		return fmt.Errorf("nats: publish: %w", err)
	}
	return nil
}

// Subscribe installs router as the dispatch table and starts the consume
// loop on the first call. Subsequent calls replace the router; the loop
// re-reads it on every message so a swap takes effect immediately.
func (a *accountingBus) Subscribe(router *bookkeeping.Router) error {
	a.mu.Lock()
	a.router = router
	start := !a.started
	a.started = true
	a.mu.Unlock()
	if !start {
		return nil
	}
	return a.bus.subscribeMessages(a.dispatch)
}

// dispatch hands msg to whatever Router is currently registered. A subject
// with no registered handler naks so it redelivers once a handler appears;
// decode and handler errors also nak so the broker retries after AckWait.
func (a *accountingBus) dispatch(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), a.bus.ackWait)
	defer cancel()

	a.mu.Lock()
	router := a.router
	a.mu.Unlock()

	subject := msg.Subject()
	if router == nil {
		_ = msg.Nak()
		return
	}
	handler := router.Handler(subject)
	if handler == nil {
		_ = msg.Nak()
		return
	}

	evt, err := decodeMsg(msg)
	if err != nil {
		_ = msg.Nak()
		return
	}
	if meta, err := msg.Metadata(); err == nil {
		ctx = accounting.WithEventMeta(ctx, accounting.EventMeta{Subject: subject, Sequence: meta.Sequence.Stream})
	}
	if err := handler.Handle(ctx, evt); err != nil {
		_ = msg.Nak()
		return
	}
	_ = msg.Ack()
}

// CatchUp blocks until the durable consumer has delivered and ack'd every
// message currently in the stream, i.e. the projection reflects the head.
func (a *accountingBus) CatchUp(ctx context.Context) error {
	return a.bus.catchUp(ctx)
}

func (a *accountingBus) Close() error {
	return a.bus.close()
}

// encodeEvent JSON-encodes a known accounting event; the json:"-" Subject and
// Sequence are not part of the event types, so the body is just the payload.
func encodeEvent(evt bookkeeping.Event) ([]byte, error) {
	switch evt.(type) {
	case accounting.JournalPosted, accounting.PeriodClosure,
		accounting.CompanyConfigured, accounting.AccountAdded,
		accounting.BranchAdded, accounting.PeriodAdded,
		accounting.PolicySet:
		body, err := json.Marshal(evt)
		if err != nil {
			return nil, fmt.Errorf("nats: marshal %s: %w", evt.EventSubject(), err)
		}
		return body, nil
	default:
		return nil, fmt.Errorf("nats: unknown event type %T for subject %q", evt, evt.EventSubject())
	}
}

// decodeMsg returns the typed event a JetStream message carries, picking the
// concrete type by the message's subject. The transport sequence is not carried
// on the event; the dispatch reads it from the message metadata into EventMeta.
func decodeMsg(msg jetstream.Msg) (bookkeeping.Event, error) {
	body := msg.Data()
	switch subject := msg.Subject(); subject {
	case accounting.SubjectJournalPosted:
		return decodeBody[accounting.JournalPosted](body)
	case accounting.SubjectPeriodClosure:
		return decodeBody[accounting.PeriodClosure](body)
	case accounting.SubjectCompanyConfigured:
		return decodeBody[accounting.CompanyConfigured](body)
	case accounting.SubjectAccountAdded:
		return decodeBody[accounting.AccountAdded](body)
	case accounting.SubjectBranchAdded:
		return decodeBody[accounting.BranchAdded](body)
	case accounting.SubjectPeriodAdded:
		return decodeBody[accounting.PeriodAdded](body)
	case accounting.SubjectPolicySet:
		return decodeBody[accounting.PolicySet](body)
	default:
		return nil, fmt.Errorf("nats: unknown subject %q", subject)
	}
}

func decodeBody[T bookkeeping.Event](body []byte) (bookkeeping.Event, error) {
	var evt T
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("nats: unmarshal event: %w", err)
	}
	return evt, nil
}
