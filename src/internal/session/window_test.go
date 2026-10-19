package session

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestInputSources(t *testing.T) {
	env := "<environment_context>\n<current_date>2026-10-14</current_date><timezone>Asia/Shanghai</timezone><filesystem><permission_profile type=\"managed\"><file_system type=\"restricted\"><entry access=\"read\"><special>:root</special></entry></file_system></permission_profile></filesystem></environment_context>"
	heart := "<heartbeat><automation_id>automation</automation_id><current_time_iso>2026-10-13T23:30:06.062Z</current_time_iso><instructions>按Asia/Shanghai时区，每周一至周五7:30生成中文晨间简报。</instructions></heartbeat>"
	for _, tc := range []struct {
		text, source string
		confirmed    bool
	}{
		{env, "environment", false}, {heart, "automation", false}, {env, "environment", true}, {heart, "automation", true},
		{"请解释这个例子：" + heart, "user", true}, {"```xml\n" + env + "\n```", "user", true},
		{env + "\n请修改日期", "user", true}, {"<heartbeat>hello</heartbeat>", "user", true}, {"<environment_context>缺少结构</environment_context>", "user", true},
		{"你好", "unconfirmed", false}, {"你好", "user", true}, {"<heartbeat><automation_id>x</automation_id>", "unconfirmed", false},
	} {
		src, _, _ := classifyInput(tc.text, tc.confirmed)
		if src != tc.source {
			t.Fatalf("%q: %s != %s", tc.text, src, tc.source)
		}
	}
	content := meta("auto", "") + event("event_msg", map[string]any{"type": "task_started", "turn_id": "t"}, 1)
	for i, text := range []string{env, heart} {
		content += event("response_item", map[string]any{"type": "message", "role": "user", "content": text}, i+2)
	}
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if m.Current.Source != "automation" || strings.Contains(m.Current.User, "<heartbeat>") || m.Current.Inputs != 1 {
		t.Fatalf("bad title: %+v", m.Current)
	}
	p, e := s.Window(k, "visual", 1, 100, 0, 0, 0, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Rows) != 2 || p.Rows[0].Kind != "environment" || p.Rows[1].Kind != "automation" || p.Rows[0].Summary == "" || !strings.Contains(p.Rows[1].Text, "<instructions>") {
		t.Fatalf("lost context: %+v", p.Rows)
	}
	list, _ := s.List(true)
	if strings.Contains(list[0].Title, "<heartbeat>") || !strings.Contains(list[0].Title, "定时触发") {
		t.Fatal("bad sidebar", list[0].Title)
	}
}

func TestVisualProjectionStableAcrossWindowsAndAppend(t *testing.T) {
	content := meta("main", "") + event("event_msg", map[string]any{"type": "task_started", "turn_id": "t"}, 1)
	for i := 0; i < 240; i++ {
		content += event("response_item", map[string]any{"type": "message", "role": "assistant", "id": fmt.Sprint("m", i), "content": fmt.Sprintf("reply %d", i)}, i+2)
	}
	content += event("response_item", map[string]any{"type": "function_call", "call_id": "command", "name": "exec_command", "arguments": "pwd"}, 245)
	content += event("response_item", map[string]any{"type": "function_call_output", "call_id": "command", "output": "/demo"}, 246)
	// Three representations of the same message are a single step; repeating the
	// same input within the same representation is a distinct input.
	content += event("response_item", map[string]any{"type": "message", "role": "user", "content": "again"}, 247)
	content += event("event_msg", map[string]any{"type": "user_message", "message": "again"}, 247)
	content += event("event_msg", map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "content": "again"}}, 247)
	content += event("event_msg", map[string]any{"type": "user_message", "message": "again"}, 248)
	s, k, path := fixture(t, content)
	m := ready(t, s, k)
	if m.Steps != 243 {
		t.Fatalf("steps %d", m.Steps)
	}
	p, e := s.Window(k, "visual", 0, 37, 0, 0, 0, false)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[int64]Row{}
	for {
		for _, r := range p.Rows {
			if _, exists := seen[r.Number]; exists {
				t.Fatalf("duplicate %d", r.Number)
			}
			seen[r.Number] = r
		}
		if !p.HasBefore {
			break
		}
		p, e = s.Window(k, "visual", 0, 37, 0, p.Before, 0, false)
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(seen) != 243 || seen[241].SourceCount != 2 || !strings.Contains(seen[241].Text, "/demo") || seen[242].SourceCount != 3 || seen[242].Source != "user" {
		t.Fatalf("projection: %d %+v %+v", len(seen), seen[241], seen[242])
	}
	for i := int64(1); i <= 243; i++ {
		if seen[i].Number != i {
			t.Fatal("gap", i)
		}
	}
	// Forward and backward ranges use exclusive cursors, never inferred page sizes.
	p, e = s.Window(k, "visual", seen[37].Line, 23, 0, 0, 0, false)
	if e != nil || p.Before != 37 || p.After != 59 {
		t.Fatalf("jump %+v %v", p, e)
	}
	q, e := s.Window(k, "visual", 0, 23, 0, 0, p.After, false)
	if e != nil || q.Before != 60 {
		t.Fatalf("after %+v %v", q, e)
	}
	old := m.Generation
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(event("response_item", map[string]any{"type": "function_call_output", "call_id": "command", "output": "continued result"}, 249))
	f.Close()
	m = ready(t, s, k)
	if m.Generation != old || m.Steps != 243 {
		t.Fatalf("append renumbered %+v", m)
	}
	p, e = s.Window(k, "visual", seen[241].Line, 1, 0, 0, 0, false)
	if e != nil || p.Rows[0].Number != 241 || p.Rows[0].SourceCount != 3 {
		t.Fatalf("append evidence %+v %v", p, e)
	}
	// Reload the cache in a fresh Store, and append another result. Correlation
	// state survives restarts without retaining decoded output in memory.
	data, root := s.Data, s.Root
	s.Close()
	fresh, e := New(root, data)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	fresh.List(true)
	if got := ready(t, fresh, k); got.Generation != old {
		t.Fatal("unexpected rebuild")
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(event("response_item", map[string]any{"type": "function_call_output", "call_id": "command", "output": "after restart"}, 250))
	f.Close()
	ready(t, fresh, k)
	p, e = fresh.Window(k, "visual", seen[241].Line, 1, 0, 0, 0, false)
	if e != nil || p.Rows[0].SourceCount != 4 || p.Rows[0].Number != 241 {
		t.Fatalf("cache lost projection %+v %v", p, e)
	}
	b, _ := json.Marshal(p.Index)
	if strings.Contains(string(b), "projection") {
		t.Fatal("internal correlations leaked in API")
	}
}

func TestWindowBudgetContinuityAndTurnBoundaries(t *testing.T) {
	content := meta("main", "")
	for turn := 1; turn <= 2; turn++ {
		content += event("event_msg", map[string]any{"type": "task_started", "turn_id": fmt.Sprint(turn)}, turn)
		for i := 0; i < 32; i++ {
			content += event("response_item", map[string]any{"type": "function_call_output", "call_id": "orphan", "output": strings.Repeat("x", 140000)}, i+3)
		}
		content += event("event_msg", map[string]any{"type": "task_complete", "turn_id": fmt.Sprint(turn)}, 40)
	}
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	for _, mode := range []string{"raw", "json", "visual"} {
		p, e := s.Window(k, mode, 0, 100, 0, 0, 0, false)
		if e != nil {
			t.Fatal(e)
		}
		next := p.Before
		count := len(p.Rows)
		for p.HasBefore {
			q, err := s.Window(k, mode, 0, 100, 0, p.Before, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if q.After != next-1 {
				t.Fatalf("%s backward gap %d -> %d", mode, next, q.After)
			}
			next = q.Before
			count += len(q.Rows)
			p = q
		}
		total := m.Lines
		if mode == "visual" {
			total = m.Steps
		}
		if int64(count) != total {
			t.Fatalf("%s count %d != %d", mode, count, total)
		}
		p, e = s.Window(k, mode, 2, 100, 1, 0, 0, false)
		if e != nil {
			t.Fatal(e)
		}
		for {
			for _, r := range p.Rows {
				if r.Turn != 1 {
					t.Fatal("turn leak")
				}
			}
			if !p.HasAfter {
				break
			}
			p, e = s.Window(k, mode, 0, 100, 1, 0, p.After, false)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestProjectionUncommittedTailRebuild(t *testing.T) {
	content := meta("crash", "") + event("response_item", map[string]any{"type": "function_call", "call_id": "x", "name": "exec_command", "arguments": "pwd"}, 1)
	s, k, path := fixture(t, content)
	old := ready(t, s, k)
	// Simulate the first write of an interrupted projection append, before its
	// manifest checkpoint. A subsequent append must rebuild, not reuse the tail.
	f, _ := os.OpenFile(s.indexPath(old, ".links"), os.O_APPEND|os.O_WRONLY, 0600)
	f.Write(make([]byte, 8))
	f.Close()
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(event("response_item", map[string]any{"type": "function_call_output", "call_id": "x", "output": "/demo"}, 2))
	f.Close()
	m := ready(t, s, k)
	if m.Generation == old.Generation {
		t.Fatal("uncommitted projection resumed")
	}
	p, e := s.Window(k, "visual", 0, 60, 0, 0, 0, false)
	if e != nil || len(p.Rows) != 1 || p.Rows[0].Number != 1 || p.Rows[0].SourceCount != 2 {
		t.Fatalf("bad recovery %+v %v", p, e)
	}
}

func TestSourceEvidenceAndLongInput(t *testing.T) {
	heart := "<heartbeat><automation_id>a</automation_id><current_time_iso>2026-10-14T00:00:00Z</current_time_iso><instructions>晨间简报" + strings.Repeat("详细规则", 9000) + "</instructions></heartbeat>"
	env := map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"text": heart}}}}
	row := Normalize(env)
	if row.Source != "automation" || !row.Truncated || len(row.Text) > 24004 {
		t.Fatalf("long input lost classification %+v", row)
	}
	content := meta("sources", "") + event("response_item", map[string]any{"type": "function_call", "call_id": "c", "name": "exec_command", "arguments": "pwd"}, 1)
	for i := 0; i < 125; i++ {
		content += event("response_item", map[string]any{"type": "function_call_output", "call_id": "c", "output": fmt.Sprint(i)}, i+2)
	}
	s, k, _ := fixture(t, content)
	ready(t, s, k)
	p, e := s.Window(k, "visual", 0, 60, 0, 0, 0, false)
	if e != nil || p.Rows[0].SourceCount != 126 || len(p.Rows[0].Sources) != 64 {
		t.Fatalf("preview %+v %v", p, e)
	}
	refs, e := s.Sources(k, 1, 0)
	if e != nil {
		t.Fatal(e)
	}
	a := refs["sources"].([]Evidence)
	if len(a) != 100 || refs["next"].(int64) == 0 {
		t.Fatal("first evidence chunk")
	}
	b, e := s.Sources(k, 1, refs["next"].(int64))
	if e != nil || len(b["sources"].([]Evidence)) != 26 || b["next"].(int64) != 0 {
		t.Fatalf("evidence continuation %v %v", b, e)
	}
	if _, e = s.Sources(k, 1, 1); e == nil {
		t.Fatal("accepted unrelated source cursor")
	}
}

func TestLongMessageMirrorsUseCompleteContent(t *testing.T) {
	prefix := strings.Repeat("long prefix ", 2000)
	content := meta("long", "")
	content += event("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"text": prefix + "A"}}}, 1)
	content += event("event_msg", map[string]any{"type": "item_completed", "item": map[string]any{"type": "AgentMessage", "content": []any{map[string]any{"text": prefix + "B"}}}}, 2)
	content += event("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"text": prefix + "B"}}}, 3)
	s, k, _ := fixture(t, content)
	m := ready(t, s, k)
	if m.Steps != 2 {
		t.Fatalf("merged unequal long messages: %d", m.Steps)
	}
	p, e := s.Window(k, "visual", 0, 60, 0, 0, 0, false)
	if e != nil || p.Rows[0].SourceCount != 1 || p.Rows[1].SourceCount != 2 {
		t.Fatalf("bad mirrors %+v %v", p, e)
	}
}
