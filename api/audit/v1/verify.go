package v1

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
		if ComputeHash(e) != e.Hash {
			summary.Breaks = append(summary.Breaks, Break{e.Instance, e.Seq, "hash does not match the event (altered)"})
		}
		s.seq, s.hash = e.Seq, e.Hash
	}
	summary.Instances = len(instances)
	return summary, scanner.Err()
}
