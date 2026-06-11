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

// Publish encodes evt, publishes it to the subject from evt.EventSubject(),
// and returns the broker-stamped event. The optimistic-concurrency hint is
// forwarded as Nats-Expected-Last-Subject-Sequence; a stale hint surfaces as
// accounting.ErrConcurrentUpdate.
func (a *accountingBus) Publish(ctx context.Context, evt bookkeeping.Event, expect accounting.ExpectedSequence) (bookkeeping.Event, error) {
	subject := evt.EventSubject()
	body, err := encodeEvent(evt)
	if err != nil {
		return nil, err
	}
	opts := []jetstream.PublishOpt{}
	if expect.Subject != "" {
		opts = append(opts, jetstream.WithExpectLastSequencePerSubject(expect.LastSeq))
	}
	seq, err := a.bus.publishRaw(ctx, subject, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return nil, accounting.ErrConcurrentUpdate
		}
		return nil, fmt.Errorf("nats: publish: %w", err)
	}
	return stamp(evt, subject, seq), nil
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

func (a *accountingBus) Close() error {
	return a.bus.close()
}

// encodeEvent JSON-encodes a known accounting event. Subject and Sequence
// have the json:"-" tag on the concrete types so they are excluded from the
// body and resupplied by the broker.
func encodeEvent(evt bookkeeping.Event) ([]byte, error) {
	switch e := evt.(type) {
	case accounting.JournalPosted:
		return encodeJournalPosted(e)
	case accounting.PeriodClosure:
		return encodePeriodClosure(e)
	default:
		return nil, fmt.Errorf("nats: unknown event type %T for subject %q", evt, evt.EventSubject())
	}
}

func encodeJournalPosted(evt accounting.JournalPosted) ([]byte, error) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("nats: marshal journal posted: %w", err)
	}
	return body, nil
}

func encodePeriodClosure(evt accounting.PeriodClosure) ([]byte, error) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("nats: marshal period closure: %w", err)
	}
	return body, nil
}

// decodeMsg returns the typed event a JetStream message carries, picking the
// concrete type by the message's subject.
func decodeMsg(msg jetstream.Msg) (bookkeeping.Event, error) {
	meta, err := msg.Metadata()
	if err != nil {
		return nil, fmt.Errorf("nats: msg metadata: %w", err)
	}
	subject := msg.Subject()
	seq := meta.Sequence.Stream
	switch subject {
	case accounting.SubjectJournalPosted:
		evt, err := decodeJournalPosted(msg.Data(), subject, seq)
		if err != nil {
			return nil, err
		}
		return evt, nil
	case accounting.SubjectPeriodClosure:
		evt, err := decodePeriodClosure(msg.Data(), subject, seq)
		if err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, fmt.Errorf("nats: unknown subject %q", subject)
	}
}

func decodeJournalPosted(body []byte, subject string, sequence uint64) (accounting.JournalPosted, error) {
	var evt accounting.JournalPosted
	if err := json.Unmarshal(body, &evt); err != nil {
		return accounting.JournalPosted{}, fmt.Errorf("nats: unmarshal journal posted: %w", err)
	}
	evt.Subject = subject
	evt.Sequence = sequence
	return evt, nil
}

func decodePeriodClosure(body []byte, subject string, sequence uint64) (accounting.PeriodClosure, error) {
	var evt accounting.PeriodClosure
	if err := json.Unmarshal(body, &evt); err != nil {
		return accounting.PeriodClosure{}, fmt.Errorf("nats: unmarshal period closure: %w", err)
	}
	evt.Subject = subject
	evt.Sequence = sequence
	return evt, nil
}

// stamp writes the transport-assigned Subject and Sequence onto whichever
// concrete type backs evt and returns it through the Event interface.
func stamp(evt bookkeeping.Event, subject string, sequence uint64) bookkeeping.Event {
	switch e := evt.(type) {
	case accounting.JournalPosted:
		e.Subject = subject
		e.Sequence = sequence
		return e
	case accounting.PeriodClosure:
		e.Subject = subject
		e.Sequence = sequence
		return e
	default:
		return evt
	}
}
