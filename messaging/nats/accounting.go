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

// accountingBus is the NATS JetStream backed bookkeeping.EventBus for the
// accounting domain. One durable consumer fans every accounting subject
// through a single ack cursor so the global publish order between subjects is
// preserved; the consume loop dispatches each message to the handler whose
// subject it carries. Broker "wrong last sequence" rejections are translated
// to accounting.ErrConcurrentUpdate so the inproc and NATS transports surface
// the same sentinel.
type accountingBus struct {
	bus *bus

	mu             sync.Mutex
	started        bool
	journalHandler bookkeeping.EventHandler
	closureHandler bookkeeping.PeriodClosureHandler
}

// NewAccountingBus opens NATS and returns a bookkeeping.EventBus configured
// for the accounting JournalPosted and PeriodClosure subjects. Close drains
// the consume loop and releases the connection.
func NewAccountingBus(ctx context.Context, cfg Config) (bookkeeping.EventBus, error) {
	b, err := connect(ctx, cfg, []string{
		bookkeeping.SubjectLedger,
		bookkeeping.SubjectPeriodClosure,
	}, streamSubjectAccounting)
	if err != nil {
		return nil, err
	}
	return &accountingBus{bus: b}, nil
}

func (a *accountingBus) Publish(ctx context.Context, evt accounting.JournalPosted, expect accounting.ExpectedSequence) (accounting.JournalPosted, error) {
	body, err := encodeAccountingEvent(evt)
	if err != nil {
		return accounting.JournalPosted{}, err
	}
	opts := []jetstream.PublishOpt{}
	if expect.Subject != "" {
		opts = append(opts, jetstream.WithExpectLastSequencePerSubject(expect.LastSeq))
	}
	seq, err := a.bus.publishRaw(ctx, bookkeeping.SubjectLedger, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return accounting.JournalPosted{}, accounting.ErrConcurrentUpdate
		}
		return accounting.JournalPosted{}, fmt.Errorf("nats: publish: %w", err)
	}
	return stampAccountingPubAck(evt, bookkeeping.SubjectLedger, seq), nil
}

func (a *accountingBus) PublishPeriodClosure(ctx context.Context, evt accounting.PeriodClosure, expect accounting.ExpectedSequence) (accounting.PeriodClosure, error) {
	body, err := encodeClosureEvent(evt)
	if err != nil {
		return accounting.PeriodClosure{}, err
	}
	opts := []jetstream.PublishOpt{}
	if expect.Subject != "" {
		opts = append(opts, jetstream.WithExpectLastSequencePerSubject(expect.LastSeq))
	}
	seq, err := a.bus.publishRaw(ctx, bookkeeping.SubjectPeriodClosure, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return accounting.PeriodClosure{}, accounting.ErrConcurrentUpdate
		}
		return accounting.PeriodClosure{}, fmt.Errorf("nats: publish: %w", err)
	}
	return stampClosurePubAck(evt, bookkeeping.SubjectPeriodClosure, seq), nil
}

// Subscribe registers handler to receive JournalPosted messages; the consume
// loop starts on the first registration so a single subscriber is enough to
// drain the stream.
func (a *accountingBus) Subscribe(handler bookkeeping.EventHandler) error {
	a.mu.Lock()
	a.journalHandler = handler
	a.mu.Unlock()
	return a.startOnce()
}

// SubscribePeriodClosure registers handler to receive PeriodClosure messages.
func (a *accountingBus) SubscribePeriodClosure(handler bookkeeping.PeriodClosureHandler) error {
	a.mu.Lock()
	a.closureHandler = handler
	a.mu.Unlock()
	return a.startOnce()
}

// startOnce starts the single consume loop. Subsequent calls are no-ops; the
// loop reads journalHandler / closureHandler under the mutex on every message
// so registrations made after the loop starts still take effect.
func (a *accountingBus) startOnce() error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	a.started = true
	a.mu.Unlock()
	return a.bus.subscribeMessages(a.dispatch)
}

// dispatch routes a JetStream message to the handler registered for its
// subject. A subject without a handler is naked so it redelivers once a
// handler registers; a decode or handler error is also naked so the broker
// retries after AckWait.
func (a *accountingBus) dispatch(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), a.bus.ackWait)
	defer cancel()

	switch msg.Subject() {
	case bookkeeping.SubjectLedger:
		a.mu.Lock()
		handler := a.journalHandler
		a.mu.Unlock()
		if handler == nil {
			_ = msg.Nak()
			return
		}
		evt, err := decodeAccountingMsg(msg)
		if err != nil {
			_ = msg.Nak()
			return
		}
		if err := handler.Handle(ctx, evt); err != nil {
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	case bookkeeping.SubjectPeriodClosure:
		a.mu.Lock()
		handler := a.closureHandler
		a.mu.Unlock()
		if handler == nil {
			_ = msg.Nak()
			return
		}
		evt, err := decodeClosureMsg(msg)
		if err != nil {
			_ = msg.Nak()
			return
		}
		if err := handler.HandlePeriodClosure(ctx, evt); err != nil {
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	default:
		// Unknown subject -- ack to avoid an infinite redelivery storm; the
		// stream's wildcard filter should never deliver something we did not
		// register a subject for, but be defensive.
		_ = msg.Ack()
	}
}

func (a *accountingBus) Close() error {
	return a.bus.close()
}

// Subject and Sequence are excluded from JSON because the transport, not the
// body, is their source of truth.
func encodeAccountingEvent(evt accounting.JournalPosted) ([]byte, error) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("nats: marshal event: %w", err)
	}
	return body, nil
}

func decodeAccountingEvent(body []byte, subject string, sequence uint64) (accounting.JournalPosted, error) {
	var evt accounting.JournalPosted
	if err := json.Unmarshal(body, &evt); err != nil {
		return accounting.JournalPosted{}, fmt.Errorf("nats: unmarshal event: %w", err)
	}
	return stampAccountingPubAck(evt, subject, sequence), nil
}

// stampAccountingPubAck stamps broker-assigned subject and sequence onto evt,
// leaving the producer-assigned Entry.ID untouched.
func stampAccountingPubAck(evt accounting.JournalPosted, subject string, sequence uint64) accounting.JournalPosted {
	evt.Subject = subject
	evt.Sequence = sequence
	return evt
}

func decodeAccountingMsg(msg jetstream.Msg) (accounting.JournalPosted, error) {
	meta, err := msg.Metadata()
	if err != nil {
		return accounting.JournalPosted{}, fmt.Errorf("nats: msg metadata: %w", err)
	}
	return decodeAccountingEvent(msg.Data(), msg.Subject(), meta.Sequence.Stream)
}

func encodeClosureEvent(evt accounting.PeriodClosure) ([]byte, error) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("nats: marshal closure: %w", err)
	}
	return body, nil
}

func decodeClosureEvent(body []byte, subject string, sequence uint64) (accounting.PeriodClosure, error) {
	var evt accounting.PeriodClosure
	if err := json.Unmarshal(body, &evt); err != nil {
		return accounting.PeriodClosure{}, fmt.Errorf("nats: unmarshal closure: %w", err)
	}
	return stampClosurePubAck(evt, subject, sequence), nil
}

func stampClosurePubAck(evt accounting.PeriodClosure, subject string, sequence uint64) accounting.PeriodClosure {
	evt.Subject = subject
	evt.Sequence = sequence
	return evt
}

func decodeClosureMsg(msg jetstream.Msg) (accounting.PeriodClosure, error) {
	meta, err := msg.Metadata()
	if err != nil {
		return accounting.PeriodClosure{}, fmt.Errorf("nats: msg metadata: %w", err)
	}
	return decodeClosureEvent(msg.Data(), msg.Subject(), meta.Sequence.Stream)
}

