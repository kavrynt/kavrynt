package v1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func chain(instance string, n int) []Event {
	prev := GenesisHash
	var events []Event
	for i := 1; i <= n; i++ {
		e := Event{Type: Type, Time: "2026-10-24T12:00:00.000Z", Instance: instance, Seq: uint64(i), RequestID: "r",
			Route: Route{Namespace: "team-a", Name: "search"}, Decision: DecisionAllow, Outcome: "ok", Status: 200, DurationMS: 1.5, PrevHash: prev}
		e.Hash = ComputeHash(e)
		prev = e.Hash
		events = append(events, e)
	}
	return events
}

func lines(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		data, _ := json.Marshal(e)
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// Verifies SPEC-0013-R03 and SPEC-0013-R06: every field is present, and the
// hash covers prev_hash and the canonical event without hash.
func TestCanonicalFormAndHash(t *testing.T) {
	e := Event{Type: Type, Seq: 1, PrevHash: GenesisHash}
	canonical := string(Canonical(e))
	want := `{"type":"kavrynt.audit.v1","time":"","instance":"","seq":1,"request_id":"","subject":"","client_id":"","issuer":"","route":{"namespace":"","name":""},"http_method":"","mcp_method":"","tool":"","decision":"","reason":"","policy_revision":"","outcome":"","status":0,"duration_ms":0,"bytes_in":0,"bytes_out":0,"prev_hash":"` + GenesisHash + `"}`
	if canonical != want {
		t.Fatalf("canonical =\n%s\nwant\n%s", canonical, want)
	}
	e.Hash = "ignored"
	if string(Canonical(e)) != want {
		t.Fatal("the canonical form includes hash")
	}
	if len(ComputeHash(e)) != 64 || ComputeHash(e) == ComputeHash(Event{Type: Type, Seq: 2, PrevHash: GenesisHash}) {
		t.Fatal("hash does not depend on the event")
	}
}

// Verifies SPEC-0013-R07.
func TestVerifyFindsBreaks(t *testing.T) {
	a, b := chain("gw-a/1", 5), chain("gw-b/1", 3)
	clean := "INFO starting\n" + lines(a[:2]) + lines(b) + "not json\n" + lines(a[2:])
	summary, err := Verify(strings.NewReader(clean))
	if err != nil || summary.Events != 8 || summary.Instances != 2 || len(summary.Breaks) != 0 {
		t.Fatalf("clean stream: %+v, %v", summary, err)
	}

	missing := lines(append(append([]Event{}, a[:2]...), a[3:]...))
	if s, _ := Verify(strings.NewReader(missing)); len(s.Breaks) == 0 || s.Breaks[0].Seq != 4 || !strings.Contains(s.Breaks[0].Problem, "1 events missing") {
		t.Fatalf("missing event: %+v", s.Breaks)
	}
	reordered := lines([]Event{a[0], a[2], a[1]})
	if s, _ := Verify(strings.NewReader(reordered)); len(s.Breaks) == 0 {
		t.Fatal("reordered events verified")
	}
	altered := append([]Event{}, a...)
	altered[2].Tool = "delete_all"
	if s, _ := Verify(strings.NewReader(lines(altered))); len(s.Breaks) != 1 || s.Breaks[0].Seq != 3 || !strings.Contains(s.Breaks[0].Problem, "altered") {
		t.Fatalf("altered event: %+v", s.Breaks)
	}
	rehashed := append([]Event{}, a...)
	rehashed[2].Tool = "delete_all"
	rehashed[2].Hash = ComputeHash(rehashed[2])
	if s, _ := Verify(strings.NewReader(lines(rehashed))); len(s.Breaks) == 0 {
		t.Fatal("an event altered and rehashed without its successors verified")
	}
	if s, _ := Verify(bytes.NewReader([]byte(lines(a[1:])))); len(s.Breaks) == 0 {
		t.Fatal("a chain without its first event verified")
	}
}
