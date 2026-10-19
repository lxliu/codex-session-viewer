package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type handle struct {
	mu      sync.RWMutex
	m       Manifest
	summary Summary
	queued  bool
}
type Store struct {
	Root, Data string
	mu         sync.RWMutex
	files      map[string]Summary
	indexes    map[string]*handle
	scanMu     sync.Mutex
	scanned    time.Time
	jobs       chan *handle
	stop       chan struct{}
	once       sync.Once
	wg         sync.WaitGroup
}

func New(root, data string) (*Store, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	data, e = filepath.Abs(data)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(data, "indexes"), 0700); e != nil {
		return nil, e
	}
	s := &Store{Root: root, Data: data, files: map[string]Summary{}, indexes: map[string]*handle{}, jobs: make(chan *handle, 1024), stop: make(chan struct{})}
	s.wg.Add(1)
	go s.worker()
	return s, nil
}
func (s *Store) Close() { s.once.Do(func() { close(s.stop) }); s.wg.Wait() }
func (s *Store) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case h := <-s.jobs:
			s.build(h)
			h.mu.Lock()
			h.queued = false
			h.mu.Unlock()
		}
	}
}
func (s *Store) List(force bool) ([]Summary, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	s.mu.RLock()
	fresh := time.Since(s.scanned) < 3*time.Second
	s.mu.RUnlock()
	if force || !fresh {
		found := map[string]Summary{}
		err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == s.Root && os.IsNotExist(err) {
					return nil
				}
				if path == s.Root {
					return err
				}
				return nil
			}
			if d.IsDir() {
				if path == s.Data || d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(path), ".jsonl") {
				return nil
			}
			st, e := d.Info()
			if e != nil || !st.Mode().IsRegular() {
				return nil
			}
			k := key(path)
			s.mu.RLock()
			old, ok := s.files[k]
			s.mu.RUnlock()
			if ok && old.Size == st.Size() && old.Modified == st.ModTime().UnixNano() {
				found[k] = old
				return nil
			}
			sum := readSummary(path, st)
			found[k] = sum
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.files = found
		s.scanned = time.Now()
		s.mu.Unlock()
	}
	s.mu.RLock()
	out := make([]Summary, 0, len(s.files))
	for _, v := range s.files {
		out = append(out, v)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}
func readSummary(path string, st os.FileInfo) Summary {
	sum := Summary{Key: key(path), Path: path, Title: filepath.Base(path), ID: filepath.Base(path), CWD: "未记录工作目录", Size: st.Size(), Modified: st.ModTime().UnixNano(), Archived: strings.Contains(filepath.ToSlash(path), "/archived_sessions/")}
	f, e := os.Open(path)
	if e != nil {
		return sum
	}
	defer f.Close()
	br := bufio.NewReaderSize(io.NewSectionReader(f, 0, min(st.Size(), 512<<10)), 64<<10)
	canonicalTitle := false
	for i := 0; i < 48; i++ {
		b, n, _, err := boundedLine(br, 256<<10)
		if n == 0 {
			break
		}
		if int64(len(b)) == n {
			if m, e := decode(b); e == nil {
				p := obj(m["payload"])
				switch str(m["type"]) {
				case "session_meta":
					sum.ID = first(p, "id", "session_id")
					if sum.ID == "" {
						sum.ID = sum.Key
					}
					sum.SessionID = str(p["session_id"])
					sum.Parent = str(p["parent_thread_id"])
					sum.Fork = str(p["forked_from_id"])
					if cwd := str(p["cwd"]); cwd != "" {
						sum.CWD = cwd
					}
					sum.Start = first(p, "timestamp")
					if sum.Start == "" {
						sum.Start = str(m["timestamp"])
					}
					sum.CLI = str(p["cli_version"])
					sum.Source = p["source"]
					sum.Role = first(p, "agent_role", "agent_type", "thread_source")
					sum.Agent = first(p, "agent_nickname", "agent_path")
					sum.HistoryBase = p["history_base"]
					sum.HistoryStart = num(p["subagent_history_start_ordinal"])
					if sum.Parent == "" {
						a := obj(obj(p["source"])["subagent"])
						sum.Parent = str(obj(a["thread_spawn"])["parent_thread_id"])
					}
				case "turn_context":
					sum.Model = str(p["model"])
				case "event_msg":
					if !canonicalTitle && str(p["type"]) == "item_completed" && str(obj(p["item"])["type"]) == "UserMessage" {
						if t := inputText(obj(p["item"])["content"]); t != "" {
							source, _, summary := classifyInput(t, true)
							if source != "environment" {
								if source == "automation" {
									sum.Title = cut(summary, 180)
								} else {
									sum.Title = cut(t, 180)
								}
								canonicalTitle = true
							}
						}
					}
					if !canonicalTitle && str(p["type"]) == "user_message" {
						if t := inputText(p["message"]); t != "" {
							source, _, summary := classifyInput(t, true)
							if source != "environment" {
								if source == "automation" {
									sum.Title = cut(summary, 180)
								} else {
									sum.Title = cut(t, 180)
								}
								canonicalTitle = true
							}
						}
					}
				case "response_item":
					if sum.Title == filepath.Base(path) && str(p["type"]) == "message" && str(p["role"]) == "user" {
						t := inputText(p["content"])
						if t != "" {
							source, _, summary := classifyInput(t, false)
							if source != "environment" {
								if source == "automation" {
									sum.Title = cut(summary, 180)
								} else {
									sum.Title = cut(t, 180)
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
		if canonicalTitle && sum.Model != "" {
			break
		}
	}
	if sum.Parent != "" && sum.Title == filepath.Base(path) {
		name := sum.Agent
		if name == "" {
			name = sum.Role
		}
		if name == "" {
			name = "子代理"
		}
		sum.Title = name + " · " + cut(sum.ID, 12)
	}
	return sum
}
func (s *Store) Find(k string) (Summary, error) {
	s.mu.RLock()
	v, ok := s.files[k]
	s.mu.RUnlock()
	if !ok {
		if _, e := s.List(false); e != nil {
			return v, e
		}
		s.mu.RLock()
		v, ok = s.files[k]
		s.mu.RUnlock()
	}
	if !ok {
		return v, os.ErrNotExist
	}
	return v, nil
}
func (s *Store) Ensure(k string) (*handle, error) {
	sum, e := s.Find(k)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(sum.Path)
	if e != nil {
		return nil, e
	}
	sum.Size = st.Size()
	sum.Modified = st.ModTime().UnixNano()
	s.mu.Lock()
	h := s.indexes[k]
	if h == nil {
		h = &handle{summary: sum}
		b, e := os.ReadFile(filepath.Join(s.Data, "indexes", k+".json"))
		if e == nil {
			var cache cachedManifest
			err := json.Unmarshal(b, &cache)
			m := cache.Manifest
			m.Projection = cache.Projection
			if err == nil && m.Version == Version && m.Path == sum.Path && s.validFiles(m) {
				m.Running = false
				h.m = m
			}
		}
		s.indexes[k] = h
	}
	s.mu.Unlock()
	h.mu.Lock()
	h.summary = sum
	needs := h.m.Version != Version || h.m.Size != sum.Size || h.m.Modified != sum.Modified || !h.m.Complete || !s.validFiles(h.m)
	if needs && !h.queued {
		h.queued = true
		h.m.Running = true
		h.m.Error = ""
		select {
		case s.jobs <- h:
		case <-s.stop:
			h.queued = false
			h.m.Running = false
		default:
			h.queued = false
			h.m.Running = false
			h.mu.Unlock()
			return nil, errors.New("索引队列已满，请稍后重试")
		}
	}
	h.mu.Unlock()
	return h, nil
}
func snapshot(h *handle) Manifest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	m := h.m
	m.Projection = make(map[string]int64, len(h.m.Projection))
	for k, v := range h.m.Projection {
		m.Projection[k] = v
	}
	if m.Current != nil {
		c := *m.Current
		c.RecentResponses = append([]string(nil), c.RecentResponses...)
		c.InputFamilies = append([]string(nil), c.InputFamilies...)
		m.Current = &c
	}
	m.Checkpoints = append([]Checkpoint(nil), m.Checkpoints...)
	return m
}
func (s *Store) State(k string) (Manifest, error) {
	h, e := s.Ensure(k)
	if e != nil {
		return Manifest{}, e
	}
	return snapshot(h), nil
}
func (s *Store) indexPath(m Manifest, suffix string) string {
	return filepath.Join(s.Data, "indexes", m.Generation+suffix)
}
func (s *Store) validFiles(m Manifest) bool {
	for suffix, n := range map[string]int64{".refs": m.Lines * RecordSize, ".turnidx": m.Turns * 16, ".visual": m.Steps * 24, ".links": m.Lines * 8} {
		st, e := os.Stat(s.indexPath(m, suffix))
		if e != nil || st.Size() < n {
			return false
		}
	}
	_, e := os.Stat(s.indexPath(m, ".turns"))
	return e == nil
}
func fingerprint(f *os.File, end int64) string {
	start := max(0, end-4096)
	b := make([]byte, end-start)
	n, _ := f.ReadAt(b, start)
	h := sha256.Sum256(b[:n])
	return hex.EncodeToString(h[:])
}
func (s *Store) persist(k string, m Manifest) error {
	m.Running = false
	b, e := json.Marshal(cachedManifest{m, m.Projection})
	if e != nil {
		return e
	}
	p := filepath.Join(s.Data, "indexes", k+".json")
	f, e := os.CreateTemp(filepath.Dir(p), "manifest-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(name, p)
}
func (s *Store) Wait(k string, timeout time.Duration) (Manifest, error) {
	deadline := time.Now().Add(timeout)
	for {
		m, e := s.State(k)
		if e != nil {
			return m, e
		}
		if m.Error != "" {
			return m, errors.New(m.Error)
		}
		if m.Complete && !m.Running {
			return m, nil
		}
		if time.Now().After(deadline) {
			return m, fmt.Errorf("index timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
