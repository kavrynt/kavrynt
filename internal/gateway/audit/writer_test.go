package audit

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Verifies SPEC-0013-R02, SPEC-0013-R06, and SPEC-0013-R09: events are JSON
// lines, chained in write order, flushed on close.
func TestWriterChainsAndFlushes(t *testing.T) {
	out := &syncBuffer{}
	w, err := New(out, "gw-a/1", Reject, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		ok := w.Reserve()
		w.Emit(auditv1.Event{RequestID: "r", Decision: auditv1.DecisionAllow}, ok)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	summary, err := auditv1.Verify(strings.NewReader(out.String()))
	if err != nil || summary.Events != 20 || len(summary.Breaks) != 0 {
		t.Fatalf("summary %+v, %v\n%s", summary, err, out.String())
	}
	if !strings.HasPrefix(out.String(), `{"type":"kavrynt.audit.v1",`) || strings.Count(out.String(), "\n") != 20 {
		t.Fatalf("output is not one JSON line per event:\n%s", out.String())
	}
	if testutil.ToFloat64(w.written) != 20 {
		t.Fatal("events written not counted")
	}
}

// blockingWriter blocks writes until released, like a stalled log pipeline.
type blockingWriter struct{ release chan struct{} }

func (b blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return len(p), nil
}

// Verifies SPEC-0013-R08: a full queue rejects new requests (default) or
// drops their events, and never blocks the caller.
func TestFullQueue(t *testing.T) {
	for _, mode := range []string{Reject, Drop} {
		release := make(chan struct{})
		w, err := New(blockingWriter{release}, "gw", mode, 3)
		if err != nil {
			t.Fatal(err)
		}
		accepted := 0
		start := time.Now()
		for i := 0; i < 10; i++ {
			ok := w.Reserve()
			if ok {
				accepted++
			}
			w.Emit(auditv1.Event{}, ok)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("%s: emitting blocked", mode)
		}
		if accepted > 4 { // three queued, one possibly taken by the writer
			t.Fatalf("%s: %d reserved with a queue of 3", mode, accepted)
		}
		if got := testutil.ToFloat64(w.overflow.WithLabelValues(mode)); got < 6 {
			t.Fatalf("%s: overflow counted %v", mode, got)
		}
		close(release)
		if err := w.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New(io.Discard, "gw", "block", 1); err == nil {
		t.Fatal("unknown overflow mode accepted")
	}
}
