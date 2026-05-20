// Package memory provides in-memory repository adapters for tests and
// single-process development. State lives in process memory and is not durable
// across restarts; for production projection storage use persistence/postgres.
//
// One file per domain (e.g. accounting.go).
package memory
