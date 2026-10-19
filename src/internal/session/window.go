package session

import (
	"os"
	"slices"
)

// Window uses exclusive cursors. Raw/JSON cursors are physical line numbers;
// visual cursors are projection step numbers. from remains a physical line for
// date/search navigation, independently of the selected display mode.
func (s *Store) Window(k, mode string, from, limit, turn, before, after int64, aux bool) (Page, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return Page{}, e
	}
	m := snapshot(h)
	if (!m.Complete || m.Running) && before == 0 && after == 0 && from == 0 && turn == 0 {
		return s.Page(k, mode, from, limit, turn)
	}
	sum, e := s.Find(k)
	if e != nil {
		return Page{}, e
	}
	p := Page{Rows: []Row{}, Total: m.Lines, Index: m}
	limit = max(1, min(limit, 100))
	f, e := os.Open(sum.Path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	refs, e := os.Open(s.indexPath(m, ".refs"))
	if e != nil {
		return p, e
	}
	defer refs.Close()
	total := m.Lines
	var steps, links *os.File
	if mode == "visual" && !aux {
		total = m.Steps
		p.Total = total
		steps, e = os.Open(s.indexPath(m, ".visual"))
		if e != nil {
			return p, e
		}
		defer steps.Close()
		links, e = os.Open(s.indexPath(m, ".links"))
		if e != nil {
			return p, e
		}
		defer links.Close()
	}
	if total == 0 {
		return p, nil
	}
	start, dir := int64(1), int64(1)
	if before > 0 {
		start = before - 1
		dir = -1
	} else if after > 0 {
		start = after + 1
	} else if from > 0 {
		start = min(from, m.Lines)
		if mode == "visual" && !aux {
			// Auxiliary source records have no step number. Locate the first step
			// starting at or after the requested source line by binary searching refs.
			lo, hi := int64(1), total+1
			for lo < hi {
				mid := (lo + hi) / 2
				sr, err := readStep(steps, mid)
				if err != nil {
					return p, err
				}
				if sr.First < from {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			start = min(lo, total)
			if r, err := readRef(refs, min(from, m.Lines)); err == nil && r.Visual > 0 {
				start = r.Visual
			}
		}
	} else {
		start = total
		dir = -1
	}
	var budget int64
	p.HasBefore = start > 1
	p.HasAfter = start < total
	for n := start; n >= 1 && n <= total && int64(len(p.Rows)) < limit && budget < PageBudget; n += dir {
		// Keep ordinary records intact at the batch byte boundary. A smaller
		// batch is preferable to making a valid record appear malformed.
		primary := n
		if mode == "visual" && !aux {
			sr, err := readStep(steps, n)
			if err != nil {
				return p, err
			}
			primary = sr.First
		}
		pref, err := readRef(refs, primary)
		if err != nil {
			return p, err
		}
		cap := int64(256 << 10)
		if mode == "visual" && !aux {
			cap = 1 << 20
		}
		if len(p.Rows) > 0 && budget+min(pref.Size, cap) > PageBudget {
			break
		}
		var r Row
		if mode == "visual" && !aux {
			r, e = s.visualRow(f, refs, steps, links, n, m.Lines, &budget)
		} else {
			var ref Ref
			ref, e = readRef(refs, n)
			if e == nil {
				var b []byte
				b, e = readAt(f, ref.Offset, ref.Size, min(256<<10, PageBudget-budget))
				budget += int64(len(b))
				r = rowFrom(b, ref, mode)
				r.Line = n
				r.Number = ref.Visual
			}
		}
		if e != nil {
			return p, e
		}
		cursor := r.Line
		if mode == "visual" && !aux {
			cursor = r.Number
		}
		if turn > 0 && r.Turn != turn {
			if dir > 0 && r.Turn > turn {
				p.HasAfter = false
				break
			}
			if dir < 0 && r.Turn < turn {
				p.HasBefore = false
				break
			}
			continue
		}
		if len(p.Rows) == 0 {
			p.Before = cursor
			p.After = cursor
		}
		p.Before = min(p.Before, cursor)
		p.After = max(p.After, cursor)
		p.Rows = append(p.Rows, r)
	}
	if dir < 0 {
		slices.Reverse(p.Rows)
	}
	if len(p.Rows) > 0 {
		p.First = p.Rows[0].Line
		p.Next = p.Rows[len(p.Rows)-1].Line + 1
		p.HasBefore = p.Before > 1
		p.HasAfter = p.After < total
		if turn > 0 {
			// Check adjacent cursor ownership without reading the source payload.
			for _, side := range []int{-1, 1} {
				n := p.Before - 1
				if side > 0 {
					n = p.After + 1
				}
				if n < 1 || n > total {
					continue
				}
				line := n
				if mode == "visual" && !aux {
					sr, err := readStep(steps, n)
					if err != nil {
						return p, err
					}
					line = sr.First
				}
				ref, err := readRef(refs, line)
				if err != nil {
					return p, err
				}
				if ref.Turn != turn {
					if side < 0 {
						p.HasBefore = false
					} else {
						p.HasAfter = false
					}
				}
			}
		}
	} else {
		p.HasBefore = false
		p.HasAfter = false
	}
	return p, nil
}
