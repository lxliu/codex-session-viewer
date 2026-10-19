package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func event(typ string, p any, sec int) string {
	b, _ := json.Marshal(map[string]any{"timestamp": fmt.Sprintf("2026-10-18T10:%02d:%02dZ", sec/60, sec%60), "type": typ, "payload": p})
	return string(b) + "\n"
}
func meta(id, parent string) string {
	return event("session_meta", map[string]any{"id": id, "session_id": "main", "parent_thread_id": parent, "cwd": "/work/demo", "cli_version": "0.160.1"}, 0)
}
func tokenMap(input, cached, output int64) map[string]any {
	return map[string]any{"input_tokens": input, "cached_input_tokens": cached, "output_tokens": output, "reasoning_output_tokens": output / 2, "total_tokens": input + output}
}
func token(turn, response string, in, cache, out int64, sec int) string {
	u := tokenMap(in, cache, out)
	return event("token_usage_record", map[string]any{"turn_id": turn, "response_id": response, "usage": u, "turn_token_usage": u, "thread_token_usage": u}, sec)
}
func fixture(t *testing.T, content string) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "nested", "sessions", "sample.jsonl")
	os.MkdirAll(filepath.Dir(path), 0700)
	if e := os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := New(root, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	list, e := s.List(true)
	if e != nil || len(list) != 1 {
		t.Fatalf("discover: %+v %v", list, e)
	}
	return s, list[0].Key, path
}
func ready(t *testing.T, s *Store, k string) Manifest {
	t.Helper()
	m, e := s.Wait(k, 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestDualUsageTurnsAndEvidence(t *testing.T) {
	content := meta("main", "") + event("event_msg", map[string]any{"type": "task_started", "turn_id": "t1"}, 1) + event("event_msg", map[string]any{"type": "item_completed", "turn_id": "t1", "item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "input_text", "text": "请完成报告"}}}}, 2) + event("response_item", map[string]any{"type": "function_call", "call_id": "call1", "name": "exec_command", "arguments": "{\"cmd\":\"pwd\"}"}, 3) + event("response_item", map[string]any{"type": "function_call_output", "call_id": "call1", "output": "/work/demo"}, 4) + token("t1", "r1", 1000, 800, 100, 5)
	content += event("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": tokenMap(1000, 800, 100), "total_token_usage": tokenMap(1000, 800, 100)}}, 6) + token("t1", "r1", 1000, 800, 100, 7)
	content += event("event_msg", map[string]any{"type": "item_completed", "turn_id": "t1", "item": map[string]any{"type": "UserMessage", "content": []any{map[string]any{"type": "input_text", "text": "再加一个表格"}}}}, 8) + event("event_msg", map[string]any{"type": "task_complete", "turn_id": "t1", "duration_ms": 9000}, 10)
	content += event("event_msg", map[string]any{"type": "task_started", "turn_id": "t2"}, 11) + event("event_msg", map[string]any{"type": "turn_aborted", "turn_id": "t2"}, 12)
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if m.Stats.Usage.Input != 1000 || m.Stats.Usage.Cached != 800 || m.Stats.Usage.Total != 1100 || m.Stats.Requests != 1 {
		t.Fatalf("double counted: %+v", m.Stats)
	}
	v, e := s.Turns(k, 1, 30)
	if e != nil {
		t.Fatal(e)
	}
	turns := v["turns"].([]Turn)
	if len(turns) != 2 || turns[0].Inputs != 2 || !strings.Contains(turns[0].User, "表格") || turns[1].Status != "interrupted" || turns[0].Duration != 9000 {
		t.Fatalf("turns: %+v", turns)
	}
	p, e := s.Page(k, "visual", 1, 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Rows) != 12 || p.Rows[3].Call != "call1" || p.Rows[4].Text != "/work/demo" {
		t.Fatalf("rows: %+v", p)
	}
	raw, e := s.Page(k, "raw", 4, 1, 0)
	if e != nil || !strings.Contains(raw.Rows[0].Raw, "exec_command") {
		t.Fatalf("evidence: %+v %v", raw, e)
	}
	line, e := s.Locate(k, "2026-10-18T10:00:08Z")
	if e != nil || line != 9 {
		t.Fatalf("locate %d %v", line, e)
	}
}
func TestLegacyUsageNoDoubleSnapshots(t *testing.T) {
	content := meta("legacy", "") + event("event_msg", map[string]any{"type": "user_message", "message": "测试"}, 1)
	for i := 0; i < 3; i++ {
		content += event("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": tokenMap(100, 40, 20)}}, 2+i)
	}
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if !m.Stats.Known || m.Stats.Usage.Total != 120 || m.Stats.Requests != 1 {
		t.Fatalf("legacy %+v", m.Stats)
	}
}
func TestIncrementalPartialReplacementCache(t *testing.T) {
	content := meta("a", "") + event("event_msg", map[string]any{"type": "task_started", "turn_id": "x"}, 1) + event("response_item", map[string]any{"type": "message", "role": "assistant", "content": strings.Repeat("a", 5000)}, 2)
	s, k, path := fixture(t, content)
	m := ready(t, s, k)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	half := `{"timestamp":"2026-10-18T10:00:03Z","type":"response_item","payload":`
	f.WriteString(half)
	f.Close()
	m2 := ready(t, s, k)
	if m2.Lines != m.Lines || m2.Generation != m.Generation {
		t.Fatalf("partial indexed or append rebuilt: %v %v", m, m2)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"type":"message","role":"assistant","content":"追加成功"}}` + "\n")
	f.Close()
	m3 := ready(t, s, k)
	if m3.Lines != m.Lines+1 || m3.Generation != m.Generation {
		t.Fatalf("increment failed: %+v", m3)
	}
	// Fresh process restores compact disk index without rescanning the log.
	reopened, e := New(s.Root, s.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopened.List(true)
	restored := ready(t, reopened, k)
	if restored.Generation != m3.Generation || restored.Lines != m3.Lines {
		t.Fatal("cache not restored")
	}
	b, _ := os.ReadFile(path)
	b = bytes.Replace(b, []byte("追加成功"), []byte("替换成功"), 1)
	os.WriteFile(path, b, 0600)
	replacement := ready(t, s, k)
	if replacement.Generation == m3.Generation {
		t.Fatal("same-size modification reused stale cache")
	}
	os.WriteFile(path, []byte(meta("a", "")), 0600)
	trunc := ready(t, s, k)
	if trunc.Lines != 1 {
		t.Fatalf("truncate: %+v", trunc)
	}
}
func TestMalformedCRLFAndMissingNewline(t *testing.T) {
	content := strings.ReplaceAll(meta("a", "")+"\nnot-json\n"+event("event_msg", map[string]any{"type": "user_message", "message": "可读"}, 1), "\n", "\r\n")
	content = strings.TrimRight(content, "\r\n")
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if m.Lines != 4 || m.Stats.Invalid != 1 || !m.NoNewline {
		t.Fatalf("malformed: %+v", m)
	}
	p, e := s.Page(k, "json", 1, 20, 0)
	if e != nil || len(p.Rows) != 4 {
		t.Fatal(p, e)
	}
	if _, e = decode([]byte(`{"x":1} trailing`)); e == nil {
		t.Fatal("trailing garbage accepted")
	}
}

func TestGenericJSONCacheDeletionAndLateUsage(t *testing.T) {
	content := meta("a", "") + event("event_msg", map[string]any{"type": "task_started", "turn_id": "first"}, 1) + token("first", "r1", 100, 40, 20, 2) + event("event_msg", map[string]any{"type": "task_started", "turn_id": "second"}, 3) + token("first", "late", 200, 80, 40, 4) + "[1,true,null,{\"large\":9007199254740993}]\n"
	s, k, path := fixture(t, content)
	m := ready(t, s, k)
	if m.Stats.Unassigned != 1 || m.Current.Known || m.Stats.Invalid != 0 {
		t.Fatalf("late record misattributed %+v", m)
	}
	p, e := s.Page(k, "json", 6, 1, 0)
	if e != nil || p.Rows[0].JSON == nil {
		t.Fatal("generic JSON not inspectable", p, e)
	}
	old := m.Generation
	os.RemoveAll(filepath.Join(s.Data, "indexes"))
	m = ready(t, s, k)
	if m.Generation == old || m.Lines != 6 {
		t.Fatal("deleted cache not rebuilt", m)
	}
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	f.WriteString(strings.Repeat(" ", 5000) + "\n" + token("second", "r2", 300, 120, 60, 5))
	f.Close()
	m = ready(t, s, k)
	before := m.Stats.Requests
	f, _ = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	f.WriteString(token("second", "r2", 300, 120, 60, 5))
	f.Close()
	m = ready(t, s, k)
	if m.Stats.Requests != before {
		t.Fatal("incremental duplicate counted", before, m.Stats.Requests)
	}
}

func TestConcurrentIndexReaders(t *testing.T) {
	content := meta("a", "")
	for i := 0; i < 500; i++ {
		content += event("event_msg", map[string]any{"type": "task_started", "turn_id": fmt.Sprint(i)}, i%60) + token(fmt.Sprint(i), fmt.Sprint(i), int64(i*100), 0, int64(i), i%60)
	}
	s, k, _ := fixture(t, content)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			m, _ := s.State(k)
			json.Marshal(m)
			s.Page(k, "visual", 0, 20, 0)
			s.Turns(k, 0, 10)
		}
	}()
	ready(t, s, k)
	<-done
}
func TestLargeLineBoundedAndLazyJSON(t *testing.T) {
	s, k, path := fixture(t, meta("a", ""))
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	prefix := `{"timestamp":"2026-10-18T10:00:01Z","type":"response_item","payload":{"type":"function_call_output","call_id":"huge","output":"`
	f.WriteString(prefix)
	block := strings.Repeat("x", 1<<20)
	for i := 0; i < 80; i++ {
		f.WriteString(block)
	}
	f.WriteString(`","after":[1,{"中文":"value"}]}}` + "\n")
	f.Close()
	m := ready(t, s, k)
	if m.Lines != 2 || m.Stats.Large != 1 || m.Stats.Invalid != 0 {
		t.Fatalf("large stats %+v", m)
	}
	p, e := s.Page(k, "json", 2, 1, 0)
	if e != nil || !p.Rows[0].Truncated || len(p.Rows[0].Raw) > 256<<10 {
		t.Fatal("unbounded page", e)
	}
	r := p.Rows[0]
	node, e := s.Inspect(context.Background(), k, r.Offset, r.Size, 0, 0, "")
	if e != nil || len(node.Children) != 3 {
		t.Fatalf("root inspect %+v %v", node, e)
	}
	payload := node.Children[2]
	n, e := s.Inspect(context.Background(), k, payload.Offset, payload.Size, 0, 0, "")
	if e != nil || len(n.Children) != 4 || n.Children[2].Size < 80<<20 || len(n.Children[2].Preview) > 1024 {
		t.Fatalf("payload inspect %+v %v", n, e)
	}
	after := n.Children[3]
	n, e = s.Inspect(context.Background(), k, after.Offset, after.Size, 0, 0, "")
	if e != nil || len(n.Children) != 2 {
		t.Fatal(n, e)
	}
	b, _, e := s.Segment(k, r.Offset+int64(len(prefix)), 64)
	if e != nil || string(b) != strings.Repeat("x", 64) {
		t.Fatal("segment", e)
	}
}
func TestJSONPaginationEscapesAndNumbers(t *testing.T) {
	var b strings.Builder
	b.WriteString(meta("a", ""))
	b.WriteString(`{"payload":[`)
	for i := 0; i < 160; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"n":%d,"s":"a\\b\"c\u4e2d"}`, 9007199254740991+int64(i))
	}
	b.WriteString("]}\n")
	s, k, _ := fixture(t, b.String())
	ready(t, s, k)
	p, _ := s.Page(k, "raw", 2, 1, 0)
	r := p.Rows[0]
	n, e := s.Inspect(context.Background(), k, r.Offset, r.Size, 0, 0, "")
	if e != nil {
		t.Fatal(e)
	}
	a := n.Children[0]
	n, e = s.Inspect(context.Background(), k, a.Offset, a.Size, 0, 0, "")
	if e != nil || len(n.Children) != 80 || n.Next == 0 {
		t.Fatal(n, e)
	}
	n2, e := s.Inspect(context.Background(), k, a.Offset, a.Size, n.Next, n.Index, n.Kind)
	if e != nil || len(n2.Children) != 80 || n2.Children[0].Key != "80" {
		t.Fatal(n2, e)
	}
	leaf, e := s.Inspect(context.Background(), k, n.Children[0].Offset, n.Children[0].Size, 0, 0, "")
	if e != nil || leaf.Children[0].Preview != "9007199254740991" || !strings.Contains(leaf.Children[1].Preview, "中") {
		t.Fatal(leaf, e)
	}
}
func TestSearchAcrossChunksAndCancellation(t *testing.T) {
	prefix := strings.Repeat("a", (256<<10)-3)
	content := prefix + "NeEdLe\n" + strings.Repeat("b", 300<<10) + "\n中文搜索\n"
	s, k, _ := fixture(t, content)
	var hits []SearchUpdate
	e := s.Search(context.Background(), k, "needle", func(u SearchUpdate) error {
		if u.Snippet != "" {
			hits = append(hits, u)
		}
		return nil
	})
	if e != nil || len(hits) != 1 || hits[0].Offset != int64(len(prefix)) || hits[0].Line != 1 {
		t.Fatal(hits, e)
	}
	hits = nil
	e = s.Search(context.Background(), k, "中文搜索", func(u SearchUpdate) error {
		if u.Snippet != "" {
			hits = append(hits, u)
		}
		return nil
	})
	if e != nil || len(hits) != 1 || hits[0].Line != 3 {
		t.Fatal(hits, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.Search(ctx, k, "a", func(SearchUpdate) error { return nil }); e != context.Canceled {
		t.Fatal("not cancelled", e)
	}
}
func TestInheritedAndNonmonotonic(t *testing.T) {
	content := `{"type":"session_meta","payload":{"id":"child","parent_thread_id":"main","cwd":"/work","subagent_history_start_ordinal":10}}` + "\n" + `{"timestamp":"2026-10-18T10:00:09Z","ordinal":1,"type":"token_usage_record","payload":{"turn_id":"old","response_id":"old","usage":{"input_tokens":99999,"total_tokens":99999}}}` + "\n" + event("event_msg", map[string]any{"type": "task_started", "turn_id": "new"}, 2) + token("new", "new", 100, 20, 4, 3)
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if m.Inherited != 1 || m.Stats.Usage.Input != 100 || m.Monotonic {
		t.Fatal(m)
	}
	line, e := s.Locate(k, "2026-10-18T10:00:03Z")
	if e != nil || line != 4 {
		t.Fatal(line, e)
	}
}

func TestRealLocalFiles(t *testing.T) {
	root := os.Getenv("CODEX_SESSION_VIEWER_REAL_ROOT")
	if root == "" {
		t.Skip("set CODEX_SESSION_VIEWER_REAL_ROOT for read-only real data verification")
	}
	s, e := New(root, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	list, e := s.List(true)
	if e != nil {
		t.Fatal(e)
	}
	if len(list) == 0 {
		t.Fatal("no real files")
	}
	for _, sum := range list {
		m := ready(t, s, sum.Key)
		p, e := s.Window(sum.Key, "visual", 0, 100, 0, 0, 0, false)
		if e != nil || len(p.Rows) == 0 && m.Steps > 0 {
			t.Fatalf("%s: %v", sum.Key, e)
		}
		t.Logf("%s bytes=%d lines=%d usage=%+v large=%d invalid=%d", sum.Key, sum.Size, m.Lines, m.Stats.Usage, m.Stats.Large, m.Stats.Invalid)
	}
}

func TestSixGBPerformance(t *testing.T) {
	gb, e := strconv.Atoi(os.Getenv("CODEX_SESSION_VIEWER_PERF_GB"))
	if e != nil || gb < 1 {
		t.Skip("set CODEX_SESSION_VIEWER_PERF_GB=6 for >5GB physical-file test")
	}
	root := t.TempDir()
	path := filepath.Join(root, "six-gb.jsonl")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	bw.WriteString(meta("perf", ""))
	payload := strings.Repeat("a", (64<<10)-200)
	target := int64(gb) << 30
	var written int64
	var count int
	gen := time.Now()
	for written < target {
		record := event("response_item", map[string]any{"type": "function_call_output", "call_id": "large-output", "output": payload}, count%3600)
		n, e := bw.WriteString(record)
		if e != nil {
			t.Fatal(e)
		}
		written += int64(n)
		count++
	}
	bw.Flush()
	f.Close()
	t.Logf("fixture: bytes=%d records=%d generation=%s", written, count, time.Since(gen))
	s, e := New(root, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	list, err := s.List(true)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	k := list[0].Key
	start := time.Now()
	p, e := s.Window(k, "visual", 0, 30, 0, 0, 0, false)
	if e != nil || len(p.Rows) == 0 {
		t.Fatal("tail", e)
	}
	t.Logf("tail first paint API=%s", time.Since(start))
	m, e := s.Wait(k, 10*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	cold := time.Since(start)
	start = time.Now()
	s.Page(k, "raw", m.Lines-20, 20, 0)
	pageTime := time.Since(start)
	start = time.Now()
	visual, err := s.Window(k, "visual", 0, 60, 0, 0, 0, false)
	if err != nil || len(visual.Rows) == 0 {
		t.Fatal("visual", err)
	}
	visualTime := time.Since(start)
	start = time.Now()
	previous, err := s.Window(k, "visual", 0, 60, 0, visual.Before, 0, false)
	if err != nil || previous.After != visual.Before-1 {
		t.Fatal("visual adjacency", err)
	}
	scrollTime := time.Since(start)
	start = time.Now()
	s.Locate(k, "2026-10-18T10:10:00Z")
	locate := time.Since(start)
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	reopened, e := New(root, s.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopened.List(true)
	start = time.Now()
	warm := ready(t, reopened, k)
	warmTime := time.Since(start)
	if warm.Generation != m.Generation {
		t.Fatal("warm index rebuilt")
	}
	t.Logf("RESULT bytes=%d cold=%s warm=%s page=%s visual=%s scroll=%s locate=%s heap=%d sys=%d lines=%d", written, cold, warmTime, pageTime, visualTime, scrollTime, locate, mem.HeapAlloc, mem.Sys, m.Lines)
	if mem.Sys > 2<<30 {
		t.Fatal("exceeded 2GB Go runtime memory")
	}
	if m.Lines != int64(count+1) {
		t.Fatal("line loss")
	}
}
