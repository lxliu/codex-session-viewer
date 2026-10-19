package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxliu/codex-session-viewer/internal/session"
	"github.com/lxliu/codex-session-viewer/web"
)

func line(typ string, p any, sec int) string {
	b, _ := json.Marshal(map[string]any{"timestamp": fmt.Sprintf("2026-10-18T09:30:%02dZ", sec), "type": typ, "payload": p})
	return string(b) + "\n"
}
func makeServer(t *testing.T) (*Server, *session.Store, string) {
	t.Helper()
	root := t.TempDir()
	data := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sessions", "2026", "10"), 0700)
	os.MkdirAll(filepath.Join(root, "archived_sessions"), 0700)
	content := line("session_meta", map[string]any{"id": "main-thread", "cwd": "/Users/demo/Projects/travel-planner", "cli_version": "0.160.1"}, 0) + line("event_msg", map[string]any{"type": "task_started", "turn_id": "task-1"}, 1) + line("turn_context", map[string]any{"turn_id": "task-1", "model": "gpt-6-sol"}, 1) + line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "item": map[string]any{"type": "UserMessage", "id": "user-1", "content": []any{map[string]any{"type": "input_text", "text": "帮我做一个旅行计划页面，要能按预算筛选目的地，风格简洁一些。"}}}}, 2)
	content += line("response_item", map[string]any{"type": "reasoning", "id": "reason-1", "summary": []any{map[string]any{"type": "summary_text", "text": "先了解项目现有结构与页面组件，再实现筛选逻辑并验证交互。"}}}, 3)
	content += line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "started_at_ms": 1792315804000, "completed_at_ms": 1792315804300, "item": map[string]any{"type": "CommandExecution", "id": "cmd-1", "command": "ls src && cat package.json", "status": "completed", "exit_code": 0, "aggregated_output": "src/\n  app/\n  components/\n  data/\n\n项目使用已有的路由和页面组件。", "duration_ms": 300}}, 4)
	content += line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "item": map[string]any{"type": "Plan", "id": "plan-1", "text": "1. 整理目的地与预算数据\n2. 实现筛选和目的地卡片\n3. 验证页面和筛选交互"}}, 5)
	content += line("response_item", map[string]any{"type": "function_call", "namespace": "collaboration", "name": "spawn_agent", "call_id": "spawn-1", "arguments": "{\"task_name\":\"review\",\"message\":\"请检查预算筛选的边界情况\"}"}, 6)
	content += line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "started_at_ms": 1792315807000, "completed_at_ms": 1792315811000, "item": map[string]any{"type": "FileChange", "id": "edit-1", "status": "completed", "changes": map[string]any{"src/components/DestinationFilter.tsx": map[string]any{"kind": "update", "diff": "+ const filtered = destinations.filter(item => item.price <= budget);\n+ return <DestinationGrid items={filtered} />;"}}}}, 11)
	content += line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "started_at_ms": 1792315812000, "completed_at_ms": 1792315817000, "item": map[string]any{"type": "CommandExecution", "id": "cmd-2", "command": "npm test -- --run", "exit_code": 0, "aggregated_output": "PASS 预算边界、空结果、重置筛选\nTests: 8 passed", "duration_ms": 5000}}, 17)
	u := map[string]any{"input_tokens": 45280, "cached_input_tokens": 38400, "output_tokens": 2480, "reasoning_output_tokens": 720, "total_tokens": 47760}
	content += line("token_usage_record", map[string]any{"turn_id": "task-1", "response_id": "response-1", "usage": u, "turn_token_usage": u, "thread_token_usage": u}, 18)
	content += line("event_msg", map[string]any{"type": "item_completed", "turn_id": "task-1", "item": map[string]any{"type": "AgentMessage", "id": "final-1", "phase": "final_answer", "content": []any{map[string]any{"type": "text", "text": "已完成旅行计划页面，支持按预算筛选目的地和重置条件。8 项测试全部通过。\n\n你可以继续告诉我想增加的目的地或筛选条件。"}}}}, 20) + line("event_msg", map[string]any{"type": "task_complete", "turn_id": "task-1", "duration_ms": 19000}, 20)
	path := filepath.Join(root, "sessions", "2026", "10", "main.jsonl")
	os.WriteFile(path, []byte(content), 0600)
	child := line("session_meta", map[string]any{"id": "review-thread", "session_id": "main-thread", "parent_thread_id": "main-thread", "agent_nickname": "Atlas", "agent_role": "reviewer", "cwd": "/Users/demo/Projects/travel-planner"}, 6) + line("event_msg", map[string]any{"type": "task_started", "turn_id": "child-1", "root_turn_id": "task-1"}, 7) + line("response_item", map[string]any{"type": "message", "role": "assistant", "content": "已检查：预算为零时展示空结果，不会抛出错误。"}, 16) + line("token_usage_record", map[string]any{"turn_id": "child-1", "root_turn_id": "task-1", "response_id": "child-resp-1", "usage": u, "turn_token_usage": u, "thread_token_usage": u}, 17) + line("event_msg", map[string]any{"type": "task_complete", "turn_id": "child-1", "duration_ms": 10000}, 17)
	os.WriteFile(filepath.Join(root, "archived_sessions", "child.jsonl"), []byte(child), 0600)
	s, e := session.New(root, data)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	list, e := s.List(true)
	if e != nil {
		t.Fatal(e)
	}
	var k string
	for _, x := range list {
		if _, e = s.Wait(x.Key, 10*time.Second); e != nil {
			t.Fatal(e)
		}
		if x.ID == "main-thread" {
			k = x.Key
		}
	}
	return New(s, web.Files), s, k
}
func request(v *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:10002"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	v.Handler().ServeHTTP(w, r)
	return w
}
func TestHTTPRoutesAndSecurity(t *testing.T) {
	v, s, k := makeServer(t)
	for _, p := range []string{"/", "/app.js", "/app.css", "/window.js", "/api/health", "/api/sessions", "/api/projects", "/api/session/" + k, "/api/session/" + k + "/page?mode=visual", "/api/session/" + k + "/page?mode=json", "/api/session/" + k + "/page?mode=raw", "/api/session/" + k + "/turns", "/api/session/" + k + "/locate?time=2026-10-18T09%3A30%3A12Z", "/api/session/" + k + "/segment?offset=0&length=64", "/api/session/" + k + "/search?q=%E9%A2%84%E7%AE%97"} {
		w := request(v, "GET", p, "")
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s status %d %s", p, w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
	for _, origin := range []string{"https://evil.test", "http://localhost:10002", "http://127.0.0.1:7777", "null"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:10002/api/sessions", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		v.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("bad origin allowed", origin)
		}
	}
	r := httptest.NewRequest("GET", "http://evil.test/api/sessions", nil)
	w := httptest.NewRecorder()
	v.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host allowed")
	}
	for _, p := range []string{"/api/session/missing", "/api/session/missing/page", "/api/asset?path=/etc/passwd", "/private-key"} {
		if code := request(v, "GET", p, "").Code; code != 404 {
			t.Fatal(p, code)
		}
	}
	if request(v, "POST", "/api/session/"+k, "").Code != 405 {
		t.Fatal("unsupported method")
	}
	prefs := `{"/work/a":"My Project","/work/b":"My Project"}`
	if request(v, "PUT", "/api/projects", prefs).Code != 200 {
		t.Fatal("preferences")
	}
	p := request(v, "GET", "/api/projects", "")
	if !strings.Contains(p.Body.String(), "My Project") {
		t.Fatal("preferences not persisted")
	}
	if _, e := os.Stat(filepath.Join(s.Data, "projects.json")); e != nil {
		t.Fatal(e)
	}
	download := request(v, "GET", "/api/session/"+k+"/download?offset=2&length=32", "")
	if download.Body.Len() != 32 {
		t.Fatal("download range", download.Body.Len())
	}
	sum, _ := s.Find(k)
	b, _ := os.ReadFile(sum.Path)
	if !strings.Contains(string(b), "帮我做一个旅行计划页面") {
		t.Fatal("source modified")
	}
}
func TestJSONNodeAPIAndLimits(t *testing.T) {
	v, s, k := makeServer(t)
	sum, _ := s.Find(k)
	w := request(v, "GET", fmt.Sprintf("/api/session/%s/node?offset=0&length=%d", k, sum.Size), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(v, "GET", "/api/session/"+k+"/segment?offset=0&length=999999999", "")
	var got struct {
		Bytes int `json:"bytes"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Bytes > 256<<10 {
		t.Fatal("segment unbounded")
	}
	if request(v, "GET", "/api/session/"+k+"/download?offset=999999999", "").Code != 400 {
		t.Fatal("invalid download range")
	}
	if request(v, "PUT", "/api/projects", strings.Repeat("x", 70<<10)).Code != 400 {
		t.Fatal("body limit")
	}
}
func TestSearchCancellationThroughHandler(t *testing.T) {
	v, _, k := makeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:10002/api/session/"+k+"/search?q=budget", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	v.Handler().ServeHTTP(w, r)
}

func TestGlobalSearchIncludesUserInputAndKeys(t *testing.T) {
	v, _, _ := makeServer(t)
	w := request(v, "GET", "/api/find?q=%E9%A2%84%E7%AE%97", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	count := 0
	done := false
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		var u session.SearchUpdate
		if e := json.Unmarshal([]byte(line), &u); e != nil {
			t.Fatal(e)
		}
		if u.Snippet != "" {
			if u.Key == "" || u.Title == "" {
				t.Fatal("missing session identity")
			}
			count++
		}
		done = done || u.Done
	}
	if count < 2 || !done {
		t.Fatal("global results missing", w.Body.String())
	}
}

// Export a self-contained UI fixture using real production HTTP handlers. This
// supports visual testing in environments where localhost listeners are denied.
func TestExportBrowserFixture(t *testing.T) {
	out := os.Getenv("CODEX_SESSION_VIEWER_BROWSER_FIXTURE")
	if out == "" {
		t.Skip("set CODEX_SESSION_VIEWER_BROWSER_FIXTURE path")
	}
	v, s, _ := makeServer(t)
	list, _ := s.List(false)
	responses := map[string]json.RawMessage{}
	for _, route := range []string{"/api/sessions", "/api/projects"} {
		w := request(v, "GET", route, "")
		responses[route] = append([]byte(nil), w.Body.Bytes()...)
	}
	for _, sum := range list {
		base := "/api/session/" + sum.Key
		for _, route := range []string{base, base + "/turns", base + "/page?mode=visual", base + "/page?mode=json", base + "/page?mode=raw", base + "/page?mode=visual&aux=1"} {
			w := request(v, "GET", route, "")
			responses[route] = append([]byte(nil), w.Body.Bytes()...)
		}
		p, e := s.Page(sum.Key, "raw", 1, 100, 0)
		if e != nil {
			t.Fatal(e)
		}
		var addNode func(int64, int64, int)
		addNode = func(off, size int64, depth int) {
			if depth > 8 {
				return
			}
			n, e := s.Inspect(context.Background(), sum.Key, off, size, 0, 0, "")
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(n)
			responses[fmt.Sprintf("%s/node?offset=%d&length=%d", base, off, size)] = b
			for _, c := range n.Children {
				if c.Kind == "object" || c.Kind == "array" {
					addNode(c.Offset, c.Size, depth+1)
				}
			}
		}
		for _, r := range p.Rows {
			addNode(r.Offset, r.Size, 0)
		}
	}
	data, _ := json.Marshal(responses)
	html, _ := web.Files.ReadFile("index.html")
	css, _ := web.Files.ReadFile("app.css")
	js, _ := web.Files.ReadFile("app.js")
	windowJS, _ := web.Files.ReadFile("window.js")
	html = bytes.Replace(html, []byte(`<script defer src="/window.js"></script>`), nil, 1)
	html = bytes.Replace(html, []byte(`<link rel="stylesheet" href="/app.css">`), []byte("<style>"+string(css)+"</style>"), 1)
	mock := `const FIXTURE=` + string(data) + `;window.__uiErrors=[];window.addEventListener('error',e=>{window.__uiErrors.push(e.message);document.body.insertAdjacentHTML('beforeend','<div class="error-banner">UI ERROR</div>')});window.addEventListener('unhandledrejection',e=>window.__uiErrors.push(String(e.reason)));window.fetch=async(input,opts={})=>{const u=new URL(input,location.href);let p=u.pathname;if(p==='/api/projects'&&opts.method==='PUT'){FIXTURE[p]=JSON.parse(opts.body);return new Response('{"ok":true}',{status:200})}if(p.endsWith('/page')){const mode=u.searchParams.get('mode')||'visual',aux=u.searchParams.get('aux')==='1';p+='?mode='+mode+(mode==='visual'&&aux?'&aux=1':'');const d=structuredClone(FIXTURE[p]);const key=r=>mode==='visual'&&!aux?r.number:r.line;const from=Number(u.searchParams.get('from')||0),before=Number(u.searchParams.get('before')||0),after=Number(u.searchParams.get('after')||0),limit=Number(u.searchParams.get('limit')||60),turn=Number(u.searchParams.get('turn')||0);if(turn>0)d.rows=d.rows.filter(r=>r.turn===turn);if(before)d.rows=d.rows.filter(r=>key(r)<before).slice(-limit);else if(after)d.rows=d.rows.filter(r=>key(r)>after).slice(0,limit);else if(from)d.rows=d.rows.filter(r=>r.line>=from).slice(0,limit);else d.rows=d.rows.slice(-limit);d.first=d.rows[0]?.line||0;d.before=key(d.rows[0]||{})||0;d.after=key(d.rows.at(-1)||{})||0;d.hasBefore=d.before>1;d.hasAfter=d.after<d.total;return new Response(JSON.stringify(d),{status:200})}if(p==='/api/find'){const item=FIXTURE['/api/sessions'].find(s=>!s.parent);return new Response(JSON.stringify({scanned:4096,size:4096,key:item.key,title:item.title,line:4,offset:600,snippet:'预算筛选',done:false})+'\n'+JSON.stringify({scanned:4096,size:4096,done:true})+'\n',{status:200})}if(p.endsWith('/locate'))return new Response('{"line":9}',{status:200});if(p.endsWith('/search'))return new Response('{"scanned":4096,"size":4096,"line":4,"offset":600,"snippet":"预算筛选","done":false}\n{"scanned":4096,"size":4096,"done":true}\n',{status:200});if(p.endsWith('/segment'))return new Response(JSON.stringify({text:'{"type":"response_item","payload":{"type":"function_call","name":"exec_command"}}',bytes:80,size:4096}),{status:200});if(p.endsWith('/node'))p+='?offset='+u.searchParams.get('offset')+'&length='+u.searchParams.get('length');if(FIXTURE[p])return new Response(JSON.stringify(FIXTURE[p]),{status:200});return new Response('{"error":"fixture not found"}',{status:404})};for(const m of ['pushState','replaceState']){const old=history[m].bind(history);history[m]=(a,b,u)=>old(a,b,u?'?'+String(u).split('?')[1]:undefined)};`
	html = bytes.Replace(html, []byte(`<script defer src="/app.js"></script>`), nil, 1)
	html = bytes.Replace(html, []byte("</body>"), []byte("<script>"+mock+"</script><script>"+string(windowJS)+"</script><script>"+string(js)+"</script></body>"), 1)
	if e := os.WriteFile(out, html, 0600); e != nil {
		t.Fatal(e)
	}
	t.Log("UI fixture", out)
}

func TestHTTPWindowCursors(t *testing.T) {
	v, _, k := makeServer(t)
	for _, mode := range []string{"visual", "raw", "json"} {
		base := "/api/session/" + k + "/page?mode=" + mode + "&limit=3"
		w := request(v, "GET", base, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var p session.Page
		if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
			t.Fatal(e)
		}
		if p.Tail || !p.HasBefore || p.HasAfter || len(p.Rows) != 3 {
			t.Fatalf("%s %+v", mode, p)
		}
		w = request(v, "GET", base+fmt.Sprintf("&before=%d", p.Before), "")
		var q session.Page
		if e := json.Unmarshal(w.Body.Bytes(), &q); e != nil {
			t.Fatal(e)
		}
		if q.After != p.Before-1 || !q.HasAfter {
			t.Fatalf("backward cursor %+v %+v", p, q)
		}
		w = request(v, "GET", base+fmt.Sprintf("&after=%d", q.After), "")
		var r session.Page
		json.Unmarshal(w.Body.Bytes(), &r)
		if r.Before != p.Before || r.After != p.After {
			t.Fatalf("forward cursor %+v %+v", p, r)
		}
		if mode == "visual" && p.Rows[0].Number == 0 {
			t.Fatal("missing visual numbering")
		}
	}
}
