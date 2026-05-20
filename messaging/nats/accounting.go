package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// streamSubjectAccounting is the JetStream stream's binding pattern: a wildcard
// over the accounting namespace, so future subjects need no stream reconfig.
const streamSubjectAccounting = "accounting.>"

// accountingBus is the NATS JetStream backed bookkeeping.EventBus for the
// accounting domain. It encodes JournalPosted to JSON and translates broker
// "wrong last sequence" into accounting.ErrConcurrentUpdate so the inproc and
// NATS transports surface the same sentinel.
type accountingBus struct {
	bus *bus
}

// NewAccountingBus opens NATS and returns a bookkeeping.EventBus configured for
// accounting JournalPosted events. Close drains the consume loop and releases
// the connection. The publish subject is fixed to bookkeeping.SubjectLedger.
func NewAccountingBus(ctx context.Context, cfg Config) (bookkeeping.EventBus, error) {
	b, err := connect(ctx, cfg, bookkeeping.SubjectLedger, streamSubjectAccounting)
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
	seq, err := a.bus.publishRaw(ctx, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return accounting.JournalPosted{}, accounting.ErrConcurrentUpdate
		}
		return accounting.JournalPosted{}, fmt.Errorf("nats: publish: %w", err)
	}
	return stampAccountingPubAck(evt, a.bus.subject, seq), nil
}

// Subscribe starts the consume loop. A successful handler Acks; a decode or
// handler error Naks for redelivery.
func (a *accountingBus) Subscribe(handler bookkeeping.EventHandler) error {
	return a.bus.subscribeMessages(func(msg jetstream.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), a.bus.ackWait)
		defer cancel()
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
	})
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
