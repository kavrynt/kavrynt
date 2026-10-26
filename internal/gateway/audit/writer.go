// Package audit writes the Gateway's audit events (SPEC-0013): one JSON line
// per MCP request decision on standard output, hash-chained per instance.
// Operational logs stay on standard error.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/prometheus/client_golang/prometheus"
)

// Overflow modes (SPEC-0013-R08).
const (
	// Reject refuses new requests while the queue is full: no call is served
	// without its record.
	Reject = "reject"
	// Drop serves requests and counts the events it cannot queue.
	Drop = "drop"
)

// DefaultQueueSize is the number of events waiting to be written.
const DefaultQueueSize = 10000

// Writer sequences, chains, and writes events.
type Writer struct {
	instance string
	mode     string
	out      *bufio.Writer

	// slots holds one token per queued event; Reserve takes one before the
	// request is processed, the writer returns it after writing.
	slots  chan struct{}
	events chan auditv1.Event
	done   chan struct{}
	closed atomic.Bool
	mu     sync.Mutex // guards closing events against Emit

	written  prometheus.Counter
	overflow *prometheus.CounterVec
	queued   prometheus.GaugeFunc

	// sink receives each event after it is written (SPEC-0017).
	sink atomic.Pointer[sinkHolder]
}

// Sink receives each written event as its JSON line, without the newline.
// Add must not block: the writer calls it in output order.
type Sink interface {
	Add(line []byte)
}

type sinkHolder struct{ sink Sink }

// SetSink passes every event written from now on to s, after standard
// output (SPEC-0017-R02).
func (w *Writer) SetSink(s Sink) {
	w.sink.Store(&sinkHolder{s})
}

// New starts a Writer for instance writing to out. mode is Reject or Drop.
func New(out io.Writer, instance, mode string, queueSize int) (*Writer, error) {
	if mode != Reject && mode != Drop {
		return nil, errors.New("audit overflow mode must be reject or drop")
	}
	if queueSize <= 0 {
		return nil, errors.New("audit queue size must be positive")
	}
	w := &Writer{
		instance: instance,
		mode:     mode,
		out:      bufio.NewWriter(out),
		slots:    make(chan struct{}, queueSize),
		events:   make(chan auditv1.Event, queueSize),
		done:     make(chan struct{}),
		written: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kavrynt_gateway_audit_events_total",
			Help: "Audit events written.",
		}),
		overflow: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kavrynt_gateway_audit_overflow_total",
			Help: "Requests met with a full audit queue, by action (reject or drop).",
		}, []string{"action"}),
	}
	w.queued = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "kavrynt_gateway_audit_queue_length",
		Help: "Audit events waiting to be written.",
	}, func() float64 { return float64(len(w.slots)) })
	go w.run()
	return w, nil
}

// Collectors are the writer's metrics (SPEC-0013-R09).
func (w *Writer) Collectors() []prometheus.Collector {
	return []prometheus.Collector{w.written, w.overflow, w.queued}
}

// Instance names this writer's chain.
func (w *Writer) Instance() string { return w.instance }

// Reserve takes room for one event before a request is processed. It
// reports false when the queue is full: in Reject mode the request must be
// refused; in Drop mode it is served and its event dropped.
func (w *Writer) Reserve() bool {
	select {
	case w.slots <- struct{}{}:
		return true
	default:
		w.overflow.WithLabelValues(w.mode).Inc()
		return false
	}
}

// Emit queues the event of a request whose Reserve succeeded; it never
// blocks. Events of unreserved requests (Drop mode) are discarded.
func (w *Writer) Emit(e auditv1.Event, reserved bool) {
	if !reserved {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed.Load() {
		<-w.slots
		return
	}
	w.events <- e
}

// Close writes the queued events and stops, or gives up when ctx ends
// (SPEC-0013-R09).
func (w *Writer) Close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed.Swap(true) {
		close(w.events)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run assigns seq and hashes in write order, so the chain is the output
// order (SPEC-0013-R06).
func (w *Writer) run() {
	defer close(w.done)
	var seq uint64
	prev := auditv1.GenesisHash
	for e := range w.events {
		seq++
		e.Type, e.Instance, e.Seq, e.PrevHash, e.Hash = auditv1.Type, w.instance, seq, prev, ""
		e.Hash = auditv1.ComputeHash(e)
		prev = e.Hash
		line, _ := json.Marshal(e)
		_, _ = w.out.Write(append(line, '\n'))
		_ = w.out.Flush()
		w.written.Inc()
		if holder := w.sink.Load(); holder != nil {
			holder.sink.Add(line[:len(line):len(line)])
		}
		<-w.slots
	}
}

// Mode is Reject or Drop.
func (w *Writer) Mode() string { return w.mode }
