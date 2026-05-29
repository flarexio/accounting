// Package nats provides NATS JetStream backed message-bus adapters: the
// production counterpart of messaging/inproc, with the same EventBus contract
// and optimistic-concurrency semantics (Nats-Expected-Last-Subject-Sequence).
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
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

// defaultAckWait mirrors JetStream's own default.
const defaultAckWait = 30 * time.Second

// Config carries the user-supplied connection and JetStream settings. URL,
// Stream, and Consumer are required; AckWait defaults to 30s when zero.
type Config struct {
	URL      string
	Stream   string
	Consumer string
	AckWait  time.Duration
}

// bus owns the NATS connection, JetStream context, durable consumer, and
// consume loop. One consumer fans every filtered subject in stream order
// through a single ack cursor; same-package domain factories add encoding,
// per-subject dispatch, and port adaptation on top.
type bus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	subjects []string
	consumer jetstream.Consumer
	ackWait  time.Duration

	mu      sync.Mutex
	consume jetstream.ConsumeContext
}

// connect opens NATS, attaches JetStream, and ensures the stream (bound to
// streamSubject) and one durable consumer (filtering on every subject in
// subjects) named in cfg exist before returning. Using a single consumer with
// FilterSubjects is deliberate: separate consumers per subject would track
// independent cursors and break the global ack order between subjects.
func connect(ctx context.Context, cfg Config, subjects []string, streamSubject string) (*bus, error) {
	if cfg.URL == "" || cfg.Stream == "" || cfg.Consumer == "" {
		return nil, errors.New("nats: url, stream, and consumer are required")
	}
	if len(subjects) == 0 || streamSubject == "" {
		return nil, errors.New("nats: at least one subject and a stream subject are required")
	}
	ackWait := cfg.AckWait
	if ackWait <= 0 {
		ackWait = defaultAckWait
	}
	nc, err := nats.Connect(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream context: %w", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      cfg.Stream,
		Subjects:  []string{streamSubject},
		Retention: jetstream.LimitsPolicy,
		Storage:   jetstream.FileStorage,
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: ensure stream %q: %w", cfg.Stream, err)
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, cfg.Stream, jetstream.ConsumerConfig{
		Durable:        cfg.Consumer,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckWait:        ackWait,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: ensure consumer %q: %w", cfg.Consumer, err)
	}
	return &bus{
		nc:       nc,
		js:       js,
		subjects: subjects,
		consumer: cons,
		ackWait:  ackWait,
	}, nil
}

// publishRaw publishes body to subject and returns the broker-assigned stream
// sequence.
func (b *bus) publishRaw(ctx context.Context, subject string, body []byte, opts ...jetstream.PublishOpt) (uint64, error) {
	ack, err := b.js.Publish(ctx, subject, body, opts...)
	if err != nil {
		return 0, err
	}
	return ack.Sequence, nil
}

// subscribeMessages starts the consume loop. handler receives raw JetStream
// messages and is responsible for Ack/Nak. Subscribing twice returns an error.
func (b *bus) subscribeMessages(handler func(msg jetstream.Msg)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.consume != nil {
		return errors.New("nats: bus already subscribed")
	}
	cc, err := b.consumer.Consume(handler)
	if err != nil {
		return fmt.Errorf("nats: consume: %w", err)
	}
	b.consume = cc
	return nil
}

// close drains the consume loop, then closes the connection. Drain must run
// first because Ack itself publishes over NATS. Safe to call repeatedly.
func (b *bus) close() error {
	b.mu.Lock()
	cc := b.consume
	b.consume = nil
	b.mu.Unlock()
	if cc != nil {
		cc.Drain()
		<-cc.Closed()
	}
	if b.nc != nil {
		b.nc.Close()
		b.nc = nil
	}
	return nil
}

// isWrongLastSequence reports whether err is JetStream's "wrong last sequence"
// rejection (APIError code 10071).
func isWrongLastSequence(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
	}
	return false
}
