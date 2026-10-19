package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func (s *Store) Page(k, mode string, from, limit, turn int64) (Page, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return Page{}, e
	}
	m := snapshot(h)
	sum, e := s.Find(k)
	if e != nil {
		return Page{}, e
	}
	p := Page{Rows: []Row{}, Total: m.Lines, Index: m}
	limit = max(1, min(limit, 150))
	f, e := os.Open(sum.Path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	if (!m.Complete || m.Running) && from <= 0 && turn == 0 || m.Running && !s.validFiles(m) {
		return s.tail(f, p, mode, limit)
	}
	if m.Lines == 0 {
		return p, nil
	}
	if from <= 0 {
		from = max(1, m.Lines-limit+1)
	}
	from = max(1, min(from, m.Lines))
	p.First = from
	refs, e := os.Open(s.indexPath(m, ".refs"))
	if e != nil {
		return p, e
	}
	defer refs.Close()
	var budget int64
	for line := from; line <= m.Lines && int64(len(p.Rows)) < limit; line++ {
		ref, e := readRef(refs, line)
		if e != nil {
			return p, e
		}
		if turn > 0 && ref.Turn != turn {
			if ref.Turn > turn {
				p.Next = 0
				break
			}
			continue
		}
		cap := int64(256 << 10)
		if mode == "visual" {
			cap = 1 << 20
		}
		if budget >= PageBudget {
			p.Next = line
			break
		}
		cap = min(cap, PageBudget-budget)
		b, e := readAt(f, ref.Offset, ref.Size, cap)
		if e != nil {
			return p, e
		}
		budget += int64(len(b))
		r := rowFrom(b, ref, mode)
		r.Line = line
		if len(p.Rows) == 0 {
			p.First = line
		}
		p.Rows = append(p.Rows, r)
		p.Next = line + 1
	}
	return p, nil
}
func rowFrom(b []byte, ref Ref, mode string) Row {
	r := Row{Kind: "unknown", Label: "无效或超大记录", Offset: ref.Offset, Size: ref.Size, Turn: ref.Turn, Timestamp: iso(ref.Timestamp), Truncated: int64(len(b))+2 < ref.Size}
	if mode == "raw" {
		r.Raw = string(b)
		return r
	}
	if mode == "json" {
		v, e := decodeValue(b)
		if e == nil {
			// The browser expands exact byte ranges through Inspect. Only a small
			// type label is sent here, avoiding large object graphs in the browser.
			typ := "JSON 值"
			switch value := v.(type) {
			case map[string]any:
				typ = first(value, "type")
				if typ == "" {
					typ = "object"
				}
			case []any:
				typ = "array"
			case string:
				typ = "string"
			}
			r.JSON = map[string]string{"type": typ}
			r.Label = "JSON 记录"
			return r
		}
	}
	m, e := decode(b)
	if e != nil {
		r.Raw = string(b)
		r.Text = "记录过大或尚不是完整 JSON。可以分段阅读完整原始内容。"
		return r
	}
	if mode == "json" {
		r.JSON = m
		return r
	}
	nr := Normalize(m)
	nr.Offset = r.Offset
	nr.Size = r.Size
	nr.Turn = r.Turn
	nr.Truncated = nr.Truncated || r.Truncated
	if ref.Flags == 2 {
		nr.Kind = "system"
		nr.Label = "继承的上下文 · " + nr.Label
	}
	return nr
}
func (s *Store) tail(f *os.File, p Page, mode string, limit int64) (Page, error) {
	st, e := f.Stat()
	if e != nil {
		return p, e
	}
	start := max(0, st.Size()-PageBudget)
	br := bufio.NewReaderSize(io.NewSectionReader(f, start, st.Size()-start), 64<<10)
	off := start
	if start > 0 {
		_, n, _, _ := boundedLine(br, 0)
		off += n
	}
	rows := make([]Row, 0, limit)
	for {
		b, n, _, err := boundedLine(br, 256<<10)
		if n == 0 {
			break
		}
		ref := Ref{Offset: off, Size: n}
		r := rowFrom(bytes.TrimRight(b, "\r\n"), ref, mode)
		rows = append(rows, r)
		if int64(len(rows)) > limit {
			rows = rows[1:]
		}
		off += n
		if err != nil {
			break
		}
	}
	if len(rows) == 0 && st.Size() > 0 {
		off = max(0, st.Size()-(64<<10))
		b, _ := readAt(f, off, st.Size()-off, 64<<10)
		rows = append(rows, Row{Offset: off, Size: st.Size() - off, Kind: "unknown", Label: "超长记录的末尾预览", Raw: string(b), Text: string(b), Truncated: true})
	}
	p.Rows = rows
	p.Tail = true
	return p, nil
}
func (s *Store) Turns(k string, from, limit int64) (map[string]any, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return nil, e
	}
	m := snapshot(h)
	total := m.Turns
	if m.Current != nil {
		total++
	}
	limit = max(1, min(limit, 50))
	if from <= 0 {
		from = max(1, total-limit+1)
	}
	from = max(1, from)
	out := []Turn{}
	if m.Turns > 0 {
		f, e := os.Open(s.indexPath(m, ".turns"))
		if e != nil {
			return nil, e
		}
		defer f.Close()
		refs, e := os.Open(s.indexPath(m, ".turnidx"))
		if e != nil {
			return nil, e
		}
		defer refs.Close()
		for n := from; n <= m.Turns && int64(len(out)) < limit; n++ {
			var b [16]byte
			if _, e = refs.ReadAt(b[:], (n-1)*16); e != nil {
				return nil, e
			}
			off := int64(binary.LittleEndian.Uint64(b[:8]))
			size := int64(binary.LittleEndian.Uint64(b[8:]))
			raw, e := readAt(f, off, size, 32<<10)
			if e != nil {
				return nil, e
			}
			var t Turn
			if e = json.Unmarshal(raw, &t); e != nil {
				return nil, e
			}
			out = append(out, t)
		}
	}
	if m.Current != nil && m.Current.Number >= from && int64(len(out)) < limit {
		t := *m.Current
		if !t.ExplicitDuration {
			t.Duration = max(0, stamp(t.End)-stamp(t.Start))
		}
		out = append(out, t)
	}
	return map[string]any{"turns": out, "first": from, "total": total, "index": m}, nil
}
func (s *Store) Locate(k, ts string) (int64, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return 0, e
	}
	m := snapshot(h)
	target := stamp(ts)
	if target == 0 {
		return 0, fmt.Errorf("invalid timestamp")
	}
	if m.Lines == 0 {
		return 0, nil
	}
	f, e := os.Open(s.indexPath(m, ".refs"))
	if e != nil {
		return 0, e
	}
	defer f.Close()
	if m.Monotonic {
		lo, hi := int64(1), m.Lines
		for lo < hi {
			mid := (lo + hi) / 2
			r, e := readRef(f, mid)
			if e != nil {
				return 0, e
			}
			if r.Timestamp == 0 {
				return s.locateCheckpoint(f, m, target)
			}
			if r.Timestamp < target {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo, nil
	}
	return s.locateCheckpoint(f, m, target)
}
func (s *Store) locateCheckpoint(f *os.File, m Manifest, target int64) (int64, error) {
	// Sparse checkpoints are navigation hints only. Non-monotonic logs are scanned
	// using compact disk refs to find the closest actual timestamp, not binary searched.
	best := int64(1)
	distance := int64(1 << 62)
	for line := int64(1); line <= m.Lines; line++ {
		r, e := readRef(f, line)
		if e != nil {
			return 0, e
		}
		if r.Timestamp == 0 {
			continue
		}
		d := r.Timestamp - target
		if d < 0 {
			d = -d
		}
		if d < distance {
			best = line
			distance = d
		}
	}
	return best, nil
}
func (s *Store) Segment(k string, offset, length int64) ([]byte, int64, error) {
	sum, e := s.Find(k)
	if e != nil {
		return nil, 0, e
	}
	f, e := os.Open(sum.Path)
	if e != nil {
		return nil, 0, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, 0, e
	}
	offset = max(0, min(offset, st.Size()))
	length = max(1, min(length, 256<<10))
	b := make([]byte, min(length, st.Size()-offset))
	n, e := f.ReadAt(b, offset)
	if e == io.EOF {
		e = nil
	}
	return b[:n], st.Size(), e
}

type SearchUpdate struct {
	Scanned int64  `json:"scanned"`
	Size    int64  `json:"size"`
	Line    int64  `json:"line"`
	Offset  int64  `json:"offset"`
	Snippet string `json:"snippet,omitempty"`
	Done    bool   `json:"done"`
	Limited bool   `json:"limited"`
	Error   string `json:"error,omitempty"`
	Key     string `json:"key,omitempty"`
	Title   string `json:"title,omitempty"`
}

func (s *Store) SearchAll(ctx context.Context, q string, emit func(SearchUpdate) error) error {
	list, e := s.List(false)
	if e != nil {
		return e
	}
	var total, base int64
	for _, sum := range list {
		total += sum.Size
	}
	hits := 0
	limited := fmt.Errorf("global search limit")
	for _, sum := range list {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = s.Search(ctx, sum.Key, q, func(u SearchUpdate) error {
			u.Key = sum.Key
			u.Title = sum.Title
			u.Size = total
			u.Scanned += base
			u.Done = false
			if u.Snippet != "" {
				hits++
			}
			if e := emit(u); e != nil {
				return e
			}
			if hits >= 300 {
				return limited
			}
			return nil
		})
		if e == limited {
			return emit(SearchUpdate{Scanned: base, Size: total, Done: true, Limited: true})
		}
		if e != nil {
			return e
		}
		base += sum.Size
	}
	return emit(SearchUpdate{Scanned: base, Size: total, Done: true})
}

func (s *Store) Search(ctx context.Context, k, q string, emit func(SearchUpdate) error) error {
	sum, e := s.Find(k)
	if e != nil {
		return e
	}
	q = strings.TrimSpace(q)
	if len(q) < 1 || len(q) > 512 {
		return fmt.Errorf("搜索词需要 1–512 字节")
	}
	f, e := os.Open(sum.Path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	// Search fixed chunks with overlap. Even a multi-gigabyte single line uses
	// constant memory, and matches spanning chunk boundaries are retained.
	buf := make([]byte, 256<<10)
	overlap := []byte{}
	needle := foldASCII([]byte(q))
	var scanned, line, lastMatch, lastReport int64
	line = 1
	lastMatch = -1
	matches := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := f.Read(buf)
		if n == 0 {
			if err != nil && err != io.EOF {
				return err
			}
			break
		}
		all := append(overlap, buf[:n]...)
		base := scanned - int64(len(overlap))
		low := foldASCII(all)
		pos := 0
		for {
			i := bytes.Index(low[pos:], needle)
			if i < 0 {
				break
			}
			i += pos
			abs := base + int64(i)
			if abs > lastMatch && i+len(needle) > len(overlap) {
				ln := line
				if i < len(overlap) {
					ln -= int64(bytes.Count(all[i:len(overlap)], []byte{'\n'}))
				} else {
					ln += int64(bytes.Count(all[len(overlap):i], []byte{'\n'}))
				}
				start := max(0, i-80)
				end := min(len(all), i+len(needle)+150)
				u := SearchUpdate{Scanned: scanned + int64(n), Size: st.Size(), Line: ln, Offset: abs, Snippet: strings.ToValidUTF8(string(all[start:end]), "�")}
				if e = emit(u); e != nil {
					return e
				}
				matches++
				lastMatch = abs
				if matches >= 300 {
					return emit(SearchUpdate{Scanned: scanned + int64(n), Size: st.Size(), Done: true, Limited: true})
				}
			}
			pos = i + max(1, len(needle))
			if pos >= len(low) {
				break
			}
		}
		line += int64(bytes.Count(buf[:n], []byte{'\n'}))
		scanned += int64(n)
		keep := min(len(all), len(needle)+4)
		overlap = append([]byte(nil), all[len(all)-keep:]...)
		if scanned-lastReport >= 4<<20 {
			if e = emit(SearchUpdate{Scanned: scanned, Size: st.Size()}); e != nil {
				return e
			}
			lastReport = scanned
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
	}
	return emit(SearchUpdate{Scanned: scanned, Size: st.Size(), Done: true})
}

// ASCII case folding preserves byte positions. Unicode text is matched exactly,
// avoiding incorrect offsets when Unicode lowercasing changes UTF-8 byte length.
func foldASCII(src []byte) []byte {
	dst := append([]byte(nil), src...)
	for i, c := range dst {
		if c >= 'A' && c <= 'Z' {
			dst[i] = c + 32
		}
	}
	return dst
}
func SortedCheckpoints(m Manifest) []Checkpoint {
	out := append([]Checkpoint(nil), m.Checkpoints...)
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}
