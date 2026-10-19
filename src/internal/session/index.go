package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func (s *Store) build(h *handle) {
	if e := os.MkdirAll(s.Data+"/indexes", 0700); e != nil {
		h.mu.Lock()
		h.m.Running = false
		h.m.Error = e.Error()
		h.mu.Unlock()
		return
	}
	m := snapshot(h)
	h.mu.RLock()
	sum := h.summary
	h.mu.RUnlock()
	fail := func(e error) { h.mu.Lock(); h.m.Running = false; h.m.Error = e.Error(); h.mu.Unlock() }
	f, e := os.Open(sum.Path)
	if e != nil {
		fail(e)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		fail(e)
		return
	}
	prefix := fingerprint(f, min(st.Size(), 4096))
	resume := m.Version == Version && m.Size < st.Size() && m.Offset <= st.Size() && m.Prefix == prefix && !m.NoNewline && m.Boundary == fingerprint(f, m.Offset) && s.validFiles(m) && s.projectionCommitted(m)
	if !resume {
		m = Manifest{Version: Version, Path: sum.Path, Generation: sum.Key + fmt.Sprintf("-%x", time.Now().UnixNano()), Monotonic: true}
		m.Prefix = prefix
	}
	m.Size = st.Size()
	m.Modified = st.ModTime().UnixNano()
	m.Complete = false
	m.Running = true
	m.Error = ""
	rf, e := os.OpenFile(s.indexPath(m, ".refs"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		fail(e)
		return
	}
	defer rf.Close()
	tf, e := os.OpenFile(s.indexPath(m, ".turns"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		fail(e)
		return
	}
	defer tf.Close()
	ti, e := os.OpenFile(s.indexPath(m, ".turnidx"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		fail(e)
		return
	}
	defer ti.Close()
	if e = rf.Truncate(m.Lines * RecordSize); e != nil {
		fail(e)
		return
	}
	rf.Seek(0, io.SeekEnd)
	if e = ti.Truncate(m.Turns * 16); e != nil {
		fail(e)
		return
	}
	ti.Seek(0, io.SeekEnd)
	var turnEnd int64
	if m.Turns > 0 {
		var b [16]byte
		if _, e = ti.ReadAt(b[:], (m.Turns-1)*16); e != nil {
			fail(e)
			return
		}
		turnEnd = int64(binary.LittleEndian.Uint64(b[:8]) + binary.LittleEndian.Uint64(b[8:]))
	}
	tf.Truncate(turnEnd)
	tf.Seek(0, io.SeekEnd)
	projection, e := s.openProjector(&m)
	if e != nil {
		fail(e)
		return
	}
	defer projection.Close()
	rw := bufio.NewWriterSize(rf, 256<<10)
	tw := bufio.NewWriterSize(tf, 64<<10)
	iw := bufio.NewWriterSize(ti, 64<<10)
	flushTurn := func() error {
		if m.Current == nil {
			return nil
		}
		t := *m.Current
		if !t.ExplicitDuration {
			t.Duration = max(0, stamp(t.End)-stamp(t.Start))
		}
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		var ref [16]byte
		binary.LittleEndian.PutUint64(ref[:8], uint64(turnEnd))
		binary.LittleEndian.PutUint64(ref[8:], uint64(len(b)))
		if _, err = tw.Write(b); err != nil {
			return err
		}
		if _, err = iw.Write(ref[:]); err != nil {
			return err
		}
		turnEnd += int64(len(b))
		m.Turns++
		m.Current = nil
		return nil
	}
	startTurn := func(id, ts string, line int64) error {
		if e := flushTurn(); e != nil {
			return e
		}
		if id == "" {
			id = fmt.Sprintf("inferred-%d", line)
		}
		m.Current = &Turn{Number: m.Turns + 1, ID: id, First: line, Last: line, Start: ts, End: ts, User: "未记录用户输入 / 自动执行", Status: "running"}
		return nil
	}
	publish := func(save bool) error {
		for _, w := range []*bufio.Writer{rw, tw, iw} {
			if e := w.Flush(); e != nil {
				return e
			}
		}
		m.Boundary = fingerprint(f, m.Offset)
		if m.NewKnown {
			m.Stats.Usage = m.NewUsage
			m.Stats.Known = true
		} else if m.OldKnown {
			m.Stats.Usage = m.OldUsage
			m.Stats.Known = true
		}
		h.mu.Lock()
		cp := m
		cp.Projection = make(map[string]int64, len(m.Projection))
		for k, v := range m.Projection {
			cp.Projection[k] = v
		}
		cp.Checkpoints = append([]Checkpoint(nil), m.Checkpoints...)
		if m.Current != nil {
			t := *m.Current
			t.RecentResponses = append([]string(nil), t.RecentResponses...)
			t.InputFamilies = append([]string(nil), t.InputFamilies...)
			cp.Current = &t
		}
		h.m = cp
		h.mu.Unlock()
		if save {
			return s.persist(sum.Key, m)
		}
		return nil
	}
	if e = publish(false); e != nil {
		fail(e)
		return
	}
	br := bufio.NewReaderSize(stopReader{s.stop, io.NewSectionReader(f, m.Offset, m.Size-m.Offset)}, 256<<10)
	lastPublish := time.Now()
	seen := map[string]bool{}
	lastID := ""
	if m.Current != nil {
		lastID = m.Current.ID
		for _, id := range m.Current.RecentResponses {
			seen[id] = true
		}
	}
	for {
		select {
		case <-s.stop:
			_ = publish(true)
			return
		default:
		}
		raw, n, newline, err := boundedLine(br, MaxSemantic)
		select {
		case <-s.stop:
			_ = publish(true)
			return
		default:
		}
		if n == 0 {
			if err != nil && err != io.EOF {
				fail(err)
				return
			}
			break
		}
		line := m.Lines + 1
		ref := Ref{Offset: m.Offset, Size: n}
		if m.Current != nil {
			ref.Turn = m.Current.Number
			m.Current.Last = line
		}
		var env map[string]any
		if n <= MaxSemantic {
			env, e = decode(bytes.TrimSpace(raw))
			if e != nil && json.Valid(bytes.TrimSpace(raw)) {
				env = map[string]any{"type": "generic_json"}
				e = nil
			}
		} else {
			e = fmt.Errorf("record too large")
			m.Stats.Large++
		}
		// An incomplete trailing write is retried from its starting byte on the next update.
		if !newline && e != nil && n <= MaxSemantic {
			break
		}
		if e != nil {
			if len(bytes.TrimSpace(raw)) > 0 {
				if n <= MaxSemantic {
					m.Stats.Invalid++
				}
				ref.Flags = 1
			}
		} else {
			p := obj(env["payload"])
			typ := str(env["type"])
			sub := str(p["type"])
			ts := str(env["timestamp"])
			ms := stamp(ts)
			ref.Timestamp = ms
			_, hasOrdinal := env["ordinal"]
			inherited := sum.HistoryStart > 0 && hasOrdinal && num(env["ordinal"]) < sum.HistoryStart
			if ms > 0 {
				if !inherited && (m.Stats.Start == "" || ms < stamp(m.Stats.Start)) {
					m.Stats.Start = ts
				}
				if !inherited && ms > stamp(m.Stats.End) {
					m.Stats.End = ts
				}
				if m.LastTime > 0 && ms < m.LastTime {
					m.Monotonic = false
				}
				m.LastTime = ms
				if len(m.Checkpoints) == 0 || line%1024 == 0 || ms-m.Checkpoints[len(m.Checkpoints)-1].Time >= 60000 {
					m.Checkpoints = append(m.Checkpoints, Checkpoint{line, ms, m.Offset})
					if len(m.Checkpoints) > 8192 {
						out := make([]Checkpoint, 0, 4097)
						for i, c := range m.Checkpoints {
							if i%2 == 0 {
								out = append(out, c)
							}
						}
						m.Checkpoints = out
					}
				}
			}
			if inherited {
				m.Inherited++
				ref.Flags = 2
			}
			if !inherited {
				id := str(p["turn_id"])
				isStart := typ == "event_msg" && (sub == "task_started" || sub == "turn_started")
				isContext := typ == "turn_context" && id != "" && (m.Current == nil || id != m.Current.ID)
				oldUser := typ == "event_msg" && sub == "user_message"
				if isStart && (m.Current == nil || m.Current.ID != id) || isContext || oldUser && (m.Current == nil || m.Current.Status != "running") {
					if e = startTurn(id, ts, line); e != nil {
						fail(e)
						return
					}
				}
				if m.Current == nil && typ != "session_meta" {
					if e = startTurn(id, ts, line); e != nil {
						fail(e)
						return
					}
				}
				if m.Current != nil {
					t := m.Current
					if id != "" && id != t.ID {
						m.Stats.Unassigned++
						t = &Turn{ID: id, Status: "unassigned"}
					}
					ref.Turn = t.Number
					t.Last = line
					if ts != "" {
						t.End = ts
					}
					if t.Root == "" {
						t.Root = str(p["root_turn_id"])
					}
					var user string
					if oldUser {
						user = inputText(p["message"])
					}
					if typ == "event_msg" && sub == "item_completed" && str(obj(p["item"])["type"]) == "UserMessage" {
						user = inputText(obj(p["item"])["content"])
					}
					if user != "" {
						inputTitle(t, user, true, typ+":"+sub)
					}
					if typ == "response_item" && sub == "message" && str(p["role"]) == "user" && t.Inputs == 0 {
						if u := inputText(p["content"]); u != "" {
							inputTitle(t, u, false, typ)
						}
					}
					if typ == "event_msg" && (sub == "task_complete" || sub == "turn_complete" || sub == "turn_aborted") {
						t.Status = "completed"
						if sub == "turn_aborted" {
							t.Status = "interrupted"
						}
						if _, ok := p["duration_ms"]; ok {
							t.Duration = num(p["duration_ms"])
							t.ExplicitDuration = true
						}
						if s := str(p["completed_at"]); s != "" {
							t.End = s
						}
					}
					if typ == "event_msg" && sub == "error" {
						t.Status = "failed"
					}
					if t.ID != lastID {
						seen = map[string]bool{}
						lastID = t.ID
					}
					if typ == "token_usage_record" {
						r := str(p["response_id"])
						if r == "" {
							r = fmt.Sprintf("line-%d", line)
						}
						r = key(r)
						u := usage(obj(p["usage"]))
						aggregate := usage(obj(p["thread_token_usage"]))
						if aggregate.Total > 0 {
							m.NewUsage = m.NewUsage.Max(aggregate)
						} else if !seen[r] {
							m.NewUsage = m.NewUsage.Add(u)
						}
						if !m.NewKnown {
							m.Stats.Requests = 0
						}
						m.NewKnown = true
						if !t.ModernUsage {
							t.Requests = 0
							t.Usage = Usage{}
							t.ModernUsage = true
						}
						fresh := !seen[r]
						if !seen[r] {
							m.Stats.Requests++
							t.Requests++
							if len(seen) < 32768 {
								seen[r] = true
							}
							t.RecentResponses = append(t.RecentResponses, r)
							if len(t.RecentResponses) > 128 {
								t.RecentResponses = append([]string(nil), t.RecentResponses[len(t.RecentResponses)-128:]...)
							}
						}
						if id == "" || id == t.ID {
							if tu := obj(p["turn_token_usage"]); tu != nil {
								t.Usage = t.Usage.Max(usage(tu))
							} else if fresh {
								t.Usage = t.Usage.Add(u)
							}
							t.Known = true
						}
					}
					if typ == "event_msg" && sub == "token_count" {
						if info := obj(p["info"]); info != nil {
							if total := obj(info["total_token_usage"]); total != nil {
								old := m.OldUsage
								m.OldUsage = m.OldUsage.Max(usage(total))
								m.OldKnown = true
								if !m.NewKnown {
									delta := m.OldUsage.Delta(old)
									t.Usage = t.Usage.Add(delta)
									t.Known = true
									if delta.Total > 0 || delta.Input > 0 || delta.Output > 0 {
										t.Requests++
										m.Stats.Requests++
									}
								}
							}
						}
					}
				}
				if typ == "response_item" && (sub == "function_call" || sub == "custom_tool_call") {
					m.Stats.ToolCalls++
				}
				if typ == "compacted" {
					m.Stats.Compactions++
				}
			}
		}
		if e = projection.add(env, &ref, line); e != nil {
			fail(e)
			return
		}
		if _, e = rw.Write(encodeRef(ref)); e != nil {
			fail(e)
			return
		}
		m.Lines++
		m.Offset += n
		m.NoNewline = !newline
		if time.Since(lastPublish) > 300*time.Millisecond {
			if e = publish(false); e != nil {
				fail(e)
				return
			}
			lastPublish = time.Now()
		}
		if err != nil {
			if err != io.EOF {
				fail(err)
				return
			}
			break
		}
	}
	m.Complete = true
	m.Running = false
	if e = publish(true); e != nil {
		fail(e)
		return
	}
}
