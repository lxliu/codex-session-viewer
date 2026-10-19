package session

import (
	"encoding/binary"
	"fmt"
	"os"
)

// A projection stores byte-sized references, never copies of tool output. A
// step owns a linked list of physical records, including mirrored events and
// tool results. Its sequence stays unchanged when more evidence is appended.
type stepRef struct{ First, Last, Count int64 }
type projector struct {
	steps, links *os.File
	m            *Manifest
}

func (s *Store) openProjector(m *Manifest) (*projector, error) {
	a, e := os.OpenFile(s.indexPath(*m, ".visual"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	b, e := os.OpenFile(s.indexPath(*m, ".links"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		a.Close()
		return nil, e
	}
	if e = a.Truncate(m.Steps * 24); e == nil {
		e = b.Truncate(m.Lines * 8)
	}
	if e != nil {
		a.Close()
		b.Close()
		return nil, e
	}
	if m.Projection == nil {
		m.Projection = map[string]int64{}
	}
	return &projector{a, b, m}, nil
}
func (p *projector) Close() { p.steps.Close(); p.links.Close() }
func readStep(f *os.File, n int64) (stepRef, error) {
	var b [24]byte
	_, e := f.ReadAt(b[:], (n-1)*24)
	return stepRef{int64(binary.LittleEndian.Uint64(b[:8])), int64(binary.LittleEndian.Uint64(b[8:16])), int64(binary.LittleEndian.Uint64(b[16:]))}, e
}
func (p *projector) add(env map[string]any, ref *Ref, line int64) error {
	var z [8]byte
	if _, e := p.links.WriteAt(z[:], (line-1)*8); e != nil {
		return e
	}
	if ref.Flags == 2 {
		return nil
	}
	r := Normalize(env)
	if r.Kind == "system" || r.Kind == "token" && r.Label == "用量累计快照" {
		return nil
	}
	if env == nil {
		r = Row{Kind: "unknown"}
	}
	family := str(env["type"])
	payload := obj(env["payload"])
	if family == "event_msg" {
		family += ":" + str(payload["type"])
	}
	var idkey, mirror, other string
	if r.Call != "" && r.Kind != "user" && r.Kind != "message" && r.Kind != "reasoning" && r.Kind != "token" {
		idkey = fmt.Sprintf("%d:call:%s", ref.Turn, r.Call)
	} else if r.ID != "" {
		idkey = fmt.Sprintf("%d:%s:%s", ref.Turn, r.Kind, r.ID)
	}
	n := p.m.Projection[idkey]
	if r.Kind == "result" && n == 0 {
		idkey = ""
	} // standalone outputs must not merge with each other
	if r.Kind == "user" || r.Kind == "message" || r.Kind == "reasoning" || r.Kind == "environment" || r.Kind == "automation" {
		identity := mirrorIdentity(env, r)
		base := fmt.Sprintf("%d:%s:%s:", ref.Turn, r.Kind, identity)
		mirror = base + family
		// Only opposite representations can be mirrors; repeated user messages in
		// the same representation are distinct, even when their text is identical.
		for _, f := range []string{"response_item", "event_msg:user_message", "event_msg:agent_message", "event_msg:agent_reasoning", "event_msg:item_completed"} {
			if f != family && p.m.Projection[base+f] > 0 && p.m.Projection[base+f] != p.m.Projection[mirror] {
				other = base + f
				break
			}
		}
		if n == 0 && other != "" {
			n = p.m.Projection[other]
		}
	}
	sr := stepRef{line, line, 1}
	if n > 0 {
		var e error
		sr, e = readStep(p.steps, n)
		if e != nil {
			return e
		}
		binary.LittleEndian.PutUint64(z[:], uint64(line))
		if _, e = p.links.WriteAt(z[:], (sr.Last-1)*8); e != nil {
			return e
		}
		sr.Last = line
		sr.Count++
	} else {
		p.m.Steps++
		n = p.m.Steps
	}
	ref.Visual = n
	var b [24]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(sr.First))
	binary.LittleEndian.PutUint64(b[8:16], uint64(sr.Last))
	binary.LittleEndian.PutUint64(b[16:], uint64(sr.Count))
	if _, e := p.steps.WriteAt(b[:], (n-1)*24); e != nil {
		return e
	}
	// Bounded correlation history; no unbounded map for multi-gigabyte sessions.
	if len(p.m.Projection) >= 2048 {
		p.m.Projection = map[string]int64{}
	}
	if idkey != "" {
		p.m.Projection[idkey] = n
	}
	if mirror != "" {
		p.m.Projection[mirror] = n
	}
	return nil
}

func (s *Store) visualRow(f, refs, steps, links *os.File, n int64, maxLine int64, budget *int64) (Row, error) {
	sr, e := readStep(steps, n)
	if e != nil {
		return Row{}, e
	}
	var row Row
	line := sr.First
	for i := 0; line > 0 && line <= maxLine && i < 64 && *budget < PageBudget; i++ {
		ref, e := readRef(refs, line)
		if e != nil {
			return row, e
		}
		b, e := readAt(f, ref.Offset, ref.Size, min(1<<20, PageBudget-*budget))
		if e != nil {
			return row, e
		}
		*budget += int64(len(b))
		r := rowFrom(b, ref, "visual")
		r.Line = line
		evidence := Evidence{line, ref.Offset, ref.Size}
		if i == 0 {
			row = r
		} else {
			if r.Kind == "result" {
				joined := row.Text + "\n\n工具返回\n" + r.Text
				row.Truncated = row.Truncated || r.Truncated || len(joined) > 48000
				row.Text = cut(joined, 48000)
			} else if r.Kind == row.Kind || row.Kind == "tool" {
				if r.Duration > 0 {
					row.Duration = r.Duration
				}
				if r.Start != "" {
					row.Start = r.Start
				}
				if r.End != "" {
					row.End = r.End
				}
				if r.Status != "" {
					row.Status = r.Status
				}
				if len(r.Text) > len(row.Text) {
					row.Text = r.Text
				}
				if r.Source == "user" && row.Source == "unconfirmed" {
					row.Source = r.Source
					row.Label = r.Label
				}
			}
		}
		row.Sources = append(row.Sources, evidence)
		if line == sr.Last {
			break
		}
		var bnext [8]byte
		if _, e = links.ReadAt(bnext[:], (line-1)*8); e != nil {
			return row, e
		}
		line = int64(binary.LittleEndian.Uint64(bnext[:]))
	}
	row.Number = n
	row.SourceCount = sr.Count
	if int64(len(row.Sources)) < sr.Count {
		row.Truncated = true
	}
	return row, nil
}

// An interrupted append may have changed an old step before the next manifest
// checkpoint. The new link is written first, so an uncommitted tail is visible
// in its length and forces a safe rebuild rather than resuming corrupt links.
func (s *Store) projectionCommitted(m Manifest) bool {
	for ext, n := range map[string]int64{".links": m.Lines * 8, ".visual": m.Steps * 24} {
		st, e := os.Stat(s.indexPath(m, ext))
		if e != nil || st.Size() != n {
			return false
		}
	}
	return true
}

func (s *Store) Sources(k string, step, after int64) (map[string]any, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return nil, e
	}
	m := snapshot(h)
	if step < 1 || step > m.Steps {
		return nil, fmt.Errorf("invalid step")
	}
	sf, e := os.Open(s.indexPath(m, ".visual"))
	if e != nil {
		return nil, e
	}
	defer sf.Close()
	refs, e := os.Open(s.indexPath(m, ".refs"))
	if e != nil {
		return nil, e
	}
	defer refs.Close()
	links, e := os.Open(s.indexPath(m, ".links"))
	if e != nil {
		return nil, e
	}
	defer links.Close()
	sr, e := readStep(sf, step)
	if e != nil {
		return nil, e
	}
	line := sr.First
	if after > 0 {
		if after > m.Lines {
			return nil, fmt.Errorf("invalid source cursor")
		}
		r, err := readRef(refs, after)
		if err != nil || r.Visual != step {
			return nil, fmt.Errorf("source does not belong to step")
		}
		var b [8]byte
		if _, e = links.ReadAt(b[:], (after-1)*8); e != nil {
			return nil, e
		}
		line = int64(binary.LittleEndian.Uint64(b[:]))
	}
	rows := []Evidence{}
	next := int64(0)
	for len(rows) < 100 && line > 0 && line <= m.Lines {
		r, err := readRef(refs, line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, Evidence{line, r.Offset, r.Size})
		next = line
		if line == sr.Last {
			line = 0
			break
		}
		var b [8]byte
		if _, e = links.ReadAt(b[:], (line-1)*8); e != nil {
			return nil, e
		}
		line = int64(binary.LittleEndian.Uint64(b[:]))
	}
	if line == 0 || line > m.Lines {
		next = 0
	}
	return map[string]any{"sources": rows, "next": next, "total": sr.Count}, nil
}

// Hash the full saved content, not its display preview: two long messages
// sharing the same prefix must never be merged just because previews match.
func mirrorIdentity(env map[string]any, r Row) string {
	if r.Identity != "" {
		return r.Identity
	}
	p := obj(env["payload"])
	var value any
	if str(env["type"]) == "response_item" {
		if str(p["type"]) == "reasoning" {
			value = p["summary"]
		} else {
			value = p["content"]
		}
	} else {
		switch str(p["type"]) {
		case "item_completed":
			i := obj(p["item"])
			value = i["content"]
			if str(i["type"]) == "Reasoning" {
				value = first(i, "summary_text", "raw_content")
			}
		case "agent_message", "user_message":
			value = p["message"]
		case "agent_reasoning", "agent_reasoning_raw_content":
			value = p["text"]
		}
	}
	if full := inputText(value); full != "" {
		return key(full)
	}
	return key(r.Text)
}
