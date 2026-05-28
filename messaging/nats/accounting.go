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

// closureConsumerSuffix is appended to cfg.Consumer to derive a second durable
// consumer dedicated to the PeriodClosure subject so the two event streams are
// tracked and acked independently.
const closureConsumerSuffix = "-period-closure"

// accountingBus is the NATS JetStream backed bookkeeping.EventBus for the
// accounting domain. It owns one durable consumer per event-type subject so
// JournalPosted and PeriodClosure are acked independently while still sharing
// the same JetStream stream. Broker "wrong last sequence" rejections are
// translated to accounting.ErrConcurrentUpdate so the inproc and NATS
// transports surface the same sentinel.
type accountingBus struct {
	journal *bus
	closure *bus
}

// NewAccountingBus opens NATS and returns a bookkeeping.EventBus that
// publishes JournalPosted on bookkeeping.SubjectLedger and PeriodClosure on
// bookkeeping.SubjectPeriodClosure, each via its own durable consumer (the
// PeriodClosure consumer is named cfg.Consumer + "-period-closure"). Close
// drains both consume loops and releases the connection.
func NewAccountingBus(ctx context.Context, cfg Config) (bookkeeping.EventBus, error) {
	journal, err := connect(ctx, cfg, bookkeeping.SubjectLedger, streamSubjectAccounting)
	if err != nil {
		return nil, err
	}
	closureCfg := cfg
	closureCfg.Consumer = cfg.Consumer + closureConsumerSuffix
	closure, err := connect(ctx, closureCfg, bookkeeping.SubjectPeriodClosure, streamSubjectAccounting)
	if err != nil {
		_ = journal.close()
		return nil, err
	}
	return &accountingBus{journal: journal, closure: closure}, nil
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
	seq, err := a.journal.publishRaw(ctx, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return accounting.JournalPosted{}, accounting.ErrConcurrentUpdate
		}
		return accounting.JournalPosted{}, fmt.Errorf("nats: publish: %w", err)
	}
	return stampAccountingPubAck(evt, a.journal.subject, seq), nil
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
	seq, err := a.closure.publishRaw(ctx, body, opts...)
	if err != nil {
		if isWrongLastSequence(err) {
			return accounting.PeriodClosure{}, accounting.ErrConcurrentUpdate
		}
		return accounting.PeriodClosure{}, fmt.Errorf("nats: publish: %w", err)
	}
	return stampClosurePubAck(evt, a.closure.subject, seq), nil
}

// Subscribe starts the consume loop. A successful handler Acks; a decode or
// handler error Naks for redelivery.
func (a *accountingBus) Subscribe(handler bookkeeping.EventHandler) error {
	return a.journal.subscribeMessages(func(msg jetstream.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), a.journal.ackWait)
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

func (a *accountingBus) SubscribePeriodClosure(handler bookkeeping.PeriodClosureHandler) error {
	return a.closure.subscribeMessages(func(msg jetstream.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), a.closure.ackWait)
		defer cancel()
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
	})
}

func (a *accountingBus) Close() error {
	closeErr := a.closure.close()
	journalErr := a.journal.close()
	if journalErr != nil {
		return journalErr
	}
	return closeErr
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
