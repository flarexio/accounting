// Package nats provides NATS JetStream backed message-bus adapters: the
// production counterpart of messaging/inproc, with the same EventBus contract
// and optimistic-concurrency semantics (Nats-Expected-Last-Subject-Sequence).
//
// One file per domain (e.g. accounting.go); this file holds the connection,
// stream, consumer, and drain plumbing shared across domains. Subjects and
// stream-subject patterns are domain decisions and live in the per-domain
// factory, not in Config.
package nats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

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

// catchUpPoll is how often catchUp re-reads the consumer's pending counts.
const catchUpPoll = 50 * time.Millisecond

// defaultCatchUpTimeout bounds catchUp when the caller's context has no
// deadline, so a stuck consumer (e.g. a poisoned, perpetually-Nak'd message)
// fails loudly instead of hanging.
const defaultCatchUpTimeout = 30 * time.Second

// catchUp blocks until the durable consumer has nothing left to do --
// NumPending (undelivered) and NumAckPending (delivered, awaiting ack) are both
// zero -- so the projection driven by the consume loop reflects the stream
// head. It requires the consume loop to be running (subscribeMessages called).
func (b *bus) catchUp(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCatchUpTimeout)
		defer cancel()
	}
	for {
		info, err := b.consumer.Info(ctx)
		if err != nil {
			return fmt.Errorf("nats: consumer info: %w", err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("nats: catch up: %w", ctx.Err())
		case <-time.After(catchUpPoll):
		}
	}
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
