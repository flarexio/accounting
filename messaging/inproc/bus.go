// Package inproc provides in-process message-bus adapters: same EventBus
// contract and optimistic-concurrency semantics as messaging/nats, but with no
// external infrastructure. Not suitable for multi-process production.
//
// One file per domain (e.g. accounting.go).
package inproc
