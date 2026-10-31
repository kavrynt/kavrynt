package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// Pins the limits the docs state ("What verify proves", issue #86): a chain
// cut off at its end, and one rewritten with every later hash recomputed,
// both verify. If Verify learns to detect either, update the docs.
func TestVerifyCannotDetectTruncationOrAFullRewrite(t *testing.T) {
	a := chain("gw-a/1", 5)
	if s, err := Verify(strings.NewReader(lines(a[:3]))); err != nil || len(s.Breaks) != 0 || s.Events != 3 {
		t.Fatalf("truncated chain: %+v, %v", s, err)
	}

	rewritten := append([]Event{}, a...)
	rewritten[2].Tool = "delete_all"
	prev := rewritten[1].Hash
	for i := 2; i < len(rewritten); i++ {
		rewritten[i].PrevHash = prev
		rewritten[i].Hash = ComputeHash(rewritten[i])
		prev = rewritten[i].Hash
	}
	if s, err := Verify(strings.NewReader(lines(rewritten))); err != nil || len(s.Breaks) != 0 {
		t.Fatalf("rewritten chain: %+v, %v", s, err)
	}
}

// newerChain is a chain written by a Gateway whose events have a field this
// version does not know, hashed over the bytes as written.
func newerChain(instance string, n int) []string {
	prev := GenesisHash
	var lines []string
	for i := 1; i <= n; i++ {
		e := Event{Type: Type, Time: "2026-10-31T12:00:00.000Z", Instance: instance, Seq: uint64(i), RequestID: "r",
			Route: Route{Namespace: "team-a", Name: "search"}, Decision: DecisionAllow, Outcome: "ok", Status: 200, PrevHash: prev}
		canonical := strings.Replace(string(Canonical(e)), `"issuer":"",`, `"issuer":"","future_field":"x",`, 1)
		sum := sha256.Sum256([]byte(prev + canonical))
		hash := hex.EncodeToString(sum[:])
		lines = append(lines, strings.TrimSuffix(canonical, "}")+`,"hash":"`+hash+`"}`)
		prev = hash
	}
	return lines
}

// reencode rewrites a line the way a collector might: members sorted by
// name, so "hash" is no longer last.
func reencode(t *testing.T, line string) string {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal([]byte(line), &members); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Verifies SPEC-0035-R01 and SPEC-0035-R05: events with a field this
// version does not know verify as written, and are counted.
func TestVerifyAcceptsNewerEventsAsWritten(t *testing.T) {
	newer := newerChain("gw-new/1", 3)
	summary, err := Verify(strings.NewReader(strings.Join(newer, "\n") + "\n"))
	if err != nil || summary.Events != 3 || len(summary.Breaks) != 0 || summary.UnknownFields != 3 {
		t.Fatalf("newer chain: %+v, %v", summary, err)
	}
	old := chain("gw-old/1", 2)
	if s, _ := Verify(strings.NewReader(lines(old))); len(s.Breaks) != 0 || s.UnknownFields != 0 {
		t.Fatalf("current chain: %+v", s)
	}
}

// Verifies SPEC-0035-R02: a chain of known fields whose members a collector
// reordered still verifies by re-encoding.
func TestVerifyFallsBackToReencoding(t *testing.T) {
	var rewritten []string
	for _, e := range chain("gw-a/1", 3) {
		data, _ := json.Marshal(e)
		rewritten = append(rewritten, reencode(t, string(data)))
	}
	if s, err := Verify(strings.NewReader(strings.Join(rewritten, "\n"))); err != nil || s.Events != 3 || len(s.Breaks) != 0 {
		t.Fatalf("reordered chain: %+v, %v", s, err)
	}
}

// Verifies SPEC-0035-R03: an event with unknown fields that does not
// verify is a break that names them and the remedy, never "altered"; an
// event without unknown fields is still reported as altered.
func TestUnknownFieldsAreNotCalledAltered(t *testing.T) {
	newer := newerChain("gw-new/1", 3)
	for name, broken := range map[string]string{
		"rewritten": reencode(t, newer[1]),
		"edited":    strings.Replace(newer[1], `"decision":"allow"`, `"decision":"deny"`, 1),
	} {
		s, _ := Verify(strings.NewReader(strings.Join([]string{newer[0], broken, newer[2]}, "\n")))
		if len(s.Breaks) != 1 || s.Breaks[0].Seq != 2 {
			t.Fatalf("%s: breaks %+v, want one at seq 2", name, s.Breaks)
		}
		problem := s.Breaks[0].Problem
		if !strings.Contains(problem, `"future_field"`) || !strings.Contains(problem, "newer kavryctl") || strings.Contains(problem, "altered") {
			t.Fatalf("%s: problem %q", name, problem)
		}
	}
	many := strings.Repeat("x", 100)
	if p := unknownProblem([]string{"a", "b", "c", "d", "e", "f", many}); !strings.Contains(p, "and 2 more") || strings.Contains(p, many) {
		t.Fatalf("problem not bounded: %q", p)
	}
	if p := unknownProblem([]string{many}); strings.Contains(p, many) || !strings.Contains(p, strings.Repeat("x", 64)+"...") {
		t.Fatalf("long name not truncated: %q", p)
	}

	current := chain("gw-a/1", 2)
	current[1].Tool = "delete_all"
	if s, _ := Verify(strings.NewReader(lines(current))); len(s.Breaks) != 1 || !strings.Contains(s.Breaks[0].Problem, "altered") {
		t.Fatalf("edited current event: %+v", s.Breaks)
	}
}

// Verifies SPEC-0035-R03 and SPEC-0035-R06: a member added to a written
// line is a break that names it, even
// though the known fields still re-encode to the hash, because nothing
// covers the added member; a rewritten newer line is a break too.
func TestAddedFieldsAreNotTrustedByReencoding(t *testing.T) {
	current := chain("gw-a/1", 2)
	first, _ := json.Marshal(current[0])
	second, _ := json.Marshal(current[1])
	injected := strings.Replace(string(second), `"tool":""`, `"tool":"","approved_by":"alice"`, 1)
	s, _ := Verify(strings.NewReader(string(first) + "\n" + injected + "\n"))
	if len(s.Breaks) != 1 || s.Breaks[0].Seq != 2 || !strings.Contains(s.Breaks[0].Problem, `"approved_by"`) || s.UnknownFields != 0 {
		t.Fatalf("injected field: breaks %+v, unknown %d", s.Breaks, s.UnknownFields)
	}

	newer := newerChain("gw-new/1", 2)
	if s, _ := Verify(strings.NewReader(strings.Join(newer, "\n"))); len(s.Breaks) != 0 || s.UnknownFields != 2 {
		t.Fatalf("newer chain as written: %+v", s)
	}

	// An event the Gateway refused before routing has an empty route.
	blank := Event{Type: Type, Instance: "gw-a/1", Seq: 2, PrevHash: current[0].Hash, Decision: DecisionInvalid}
	blank.Hash = ComputeHash(blank)
	blankLine, _ := json.Marshal(blank)
	// The added member comes after route, where a stopped scan would miss it.
	emptyRoute := strings.Replace(string(blankLine), `"http_method":""`, `"http_method":"","approved_by":"alice"`, 1)

	// A member added inside route, and a repeated member whose last value is
	// the original: decoding hides both, so re-encoding would match.
	for name, tc := range map[string]struct{ edited, want string }{
		"nested member":   {strings.Replace(string(second), `"name":"search"}`, `"name":"search","approved_by":"alice"}`, 1), `"route.approved_by"`},
		"repeated member": {strings.Replace(string(second), `"tool":""`, `"tool":"delete_all","tool":""`, 1), `repeats "tool"`},
		// route null decodes to an empty Route and stops the member scan,
		// which must not let a member after it through.
		// The member after it is still found and named.
		"route not an object":                {strings.Replace(emptyRoute, `"route":{"namespace":"","name":""}`, `"route":null`, 1), `"approved_by"`},
		"route not an object, nothing added": {strings.Replace(string(blankLine), `"route":{"namespace":"","name":""}`, `"route":null`, 1), "altered"},
	} {
		s, _ := Verify(strings.NewReader(string(first) + "\n" + tc.edited + "\n"))
		if len(s.Breaks) != 1 || s.Breaks[0].Seq != 2 || !strings.Contains(s.Breaks[0].Problem, tc.want) {
			t.Fatalf("%s: breaks %+v, want one naming %s", name, s.Breaks, tc.want)
		}
	}
	// A collector's re-encoding of a known-fields line still verifies.
	if s, _ := Verify(strings.NewReader(reencode(t, string(first)) + "\n" + reencode(t, string(second)) + "\n")); len(s.Breaks) != 0 {
		t.Fatalf("re-encoded chain: %+v", s.Breaks)
	}
}
