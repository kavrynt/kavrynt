package v1

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Break is one place where a chain does not verify.
type Break struct {
	Instance string
	Seq      uint64
	Problem  string
}

func (b Break) String() string {
	return fmt.Sprintf("instance %s seq %d: %s", b.Instance, b.Seq, b.Problem)
}

// Summary is the result of verifying a stream of events.
type Summary struct {
	Events    int
	Instances int
	Breaks    []Break
	// UnknownFields counts verified events with members this version does
	// not know: their hashes cover them, but they were not interpreted
	// (SPEC-0035-R05).
	UnknownFields int
}

// hashMember is the last member of every line the Gateway writes
// (SPEC-0035-R04).
var hashMember = regexp.MustCompile(`,"hash":"[0-9a-f]{64}"}$`)

// knownFields are the members of Event, from its JSON tags.
var knownFields = func() map[string]bool {
	known := map[string]bool{}
	t := reflect.TypeOf(Event{})
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		known[name] = true
	}
	return known
}()

// verifiesAsWritten reports whether line, without its hash member, hashes
// with e.PrevHash to e.Hash: the bytes the Gateway hashed, whatever members
// they hold (SPEC-0035-R01).
func verifiesAsWritten(line string, e Event) bool {
	loc := hashMember.FindStringIndex(line)
	if loc == nil {
		return false
	}
	sum := sha256.New()
	sum.Write([]byte(e.PrevHash))
	sum.Write([]byte(line[:loc[0]]))
	sum.Write([]byte("}"))
	return hex.EncodeToString(sum.Sum(nil)) == e.Hash
}

// routeFields are the members of Route, from its JSON tags.
var routeFields = map[string]bool{"namespace": true, "name": true}

// members walks line's JSON tokens and returns, sorted, the members Event
// does not have (at the top level and inside route, as "route.<name>") and
// the members an object repeats. Decoding keeps only the last of repeated
// members and drops unknown ones, so neither shows in the decoded Event.
// complete is false when the scan could not read the whole line (for
// example a route that is not an object): members after that point are
// unseen.
func members(line string) (unknown, repeated []string, complete bool) {
	dec := json.NewDecoder(strings.NewReader(line))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, false
	}
	var routeNotObject bool
	complete = walkMembers(dec, "", knownFields, &unknown, &repeated, &routeNotObject) && !routeNotObject
	slices.Sort(unknown)
	slices.Sort(repeated)
	return unknown, repeated, complete
}

// walkMembers reads the members of an object whose opening brace was read,
// through its closing brace. It reports false on malformed input.
func walkMembers(dec *json.Decoder, prefix string, known map[string]bool, unknown, repeated *[]string, notObject *bool) bool {
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return false
		}
		name, _ := t.(string)
		path := prefix + name
		if seen[name] {
			*repeated = append(*repeated, path)
		}
		seen[name] = true
		if !known[name] {
			*unknown = append(*unknown, path)
		}
		if prefix == "" && name == "route" {
			// The Gateway always writes route as an object. Anything else
			// (null decodes to an empty Route) makes the scan incomplete,
			// which refuses re-encoding, but the members after it are still
			// read so they can be named.
			var value json.RawMessage
			if dec.Decode(&value) != nil {
				return false
			}
			sub := json.NewDecoder(bytes.NewReader(value))
			if t, err := sub.Token(); err != nil || t != json.Delim('{') {
				*notObject = true
				continue
			}
			if !walkMembers(sub, "route.", routeFields, unknown, repeated, notObject) {
				return false
			}
			continue
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return false
		}
	}
	_, err := dec.Token()
	return err == nil
}

// quoted quotes names for a break message, at most five, each bounded.
func quoted(names []string) []string {
	out := make([]string, 0, 6)
	for _, name := range names[:min(len(names), 5)] {
		if len(name) > 64 {
			name = name[:64] + "..."
		}
		out = append(out, fmt.Sprintf("%q", name))
	}
	if len(names) > 5 {
		out = append(out, fmt.Sprintf("and %d more", len(names)-5))
	}
	return out
}

// unknownProblem explains a break of an event with unknown members, naming
// at most five of them, quoted and bounded (SPEC-0035-R03).
func unknownProblem(unknown []string) string {
	return "hash does not match, and the event has fields this kavryctl does not know (" +
		strings.Join(quoted(unknown), ", ") + "), so it cannot tell an edit from a rewrite on the way: " +
		"verify the original lines with the Gateway's release or a newer kavryctl"
}

// Verify reads lines from r, skips lines that are not version 1 audit
// events (log collectors interleave other output), and checks every
// instance's chain: seq starts at 1 and increases by one, prev_hash is the
// previous event's hash, and hash matches the event (SPEC-0013-R07).
func Verify(r io.Reader) (Summary, error) {
	type state struct {
		seq  uint64
		hash string
	}
	instances := map[string]*state{}
	var summary Summary
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"type":"`+Type+`"`) {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(line), &e) != nil || e.Type != Type {
			continue
		}
		summary.Events++
		s := instances[e.Instance]
		if s == nil {
			s = &state{hash: GenesisHash}
			instances[e.Instance] = s
		}
		if e.Seq != s.seq+1 {
			problem := fmt.Sprintf("expected seq %d", s.seq+1)
			if e.Seq <= s.seq {
				problem += " (reordered or repeated)"
			} else {
				problem += fmt.Sprintf(" (%d events missing)", e.Seq-s.seq-1)
			}
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, problem})
		} else if e.PrevHash != s.hash {
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, "prev_hash does not match the previous event"})
		}
		unknown, repeated, complete := members(line)
		// The written line first, then the known fields re-encoded, for
		// lines a collector rewrote (SPEC-0035-R02). Re-encoding covers only
		// what decoding kept, so a line with unknown members (at any level
		// Event has) or repeated members must verify as written: either
		// could be added after the Gateway hashed the line (SPEC-0035-R06).
		switch {
		case verifiesAsWritten(line, e):
			if len(unknown) > 0 {
				summary.UnknownFields++
			}
		case complete && len(unknown) == 0 && len(repeated) == 0 && ComputeHash(e) == e.Hash:
		case len(repeated) > 0:
			// The Gateway never repeats a member; readers disagree on which
			// value counts.
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, "hash does not match the event (altered): it repeats " + strings.Join(quoted(repeated), ", ")})
		case len(unknown) > 0:
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, unknownProblem(unknown)})
		default:
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, "hash does not match the event (altered)"})
		}
		s.seq, s.hash = e.Seq, e.Hash
	}
	summary.Instances = len(instances)
	return summary, scanner.Err()
}
