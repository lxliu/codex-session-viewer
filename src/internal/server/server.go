package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/lxliu/codex-session-viewer/internal/session"
)

type Server struct {
	store *session.Store
	web   fs.FS
	mux   *http.ServeMux
	slots chan struct{}
}

func New(s *session.Store, web fs.FS) *Server {
	v := &Server{store: s, web: web, mux: http.NewServeMux(), slots: make(chan struct{}, 3)}
	v.routes()
	return v
}
func (v *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "仅允许本机访问", 403)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, e := url.Parse(o)
			if e != nil || u.Host != r.Host || u.Scheme != "http" {
				http.Error(w, "拒绝跨来源请求", 403)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "拒绝跨站请求", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasSuffix(r.URL.Path, "/search") && r.URL.Path != "/api/find" {
			select {
			case v.slots <- struct{}{}:
				defer func() { <-v.slots }()
			case <-r.Context().Done():
				return
			}
		}
		defer func() {
			if e := recover(); e != nil {
				log.Printf("request panic %s: %v", r.URL.Path, e)
				http.Error(w, "请求处理失败", 500)
			}
		}()
		v.mux.ServeHTTP(w, r)
	})
}
func (v *Server) routes() {
	v.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"ok": true, "app": "codex-session-viewer", "root": v.store.Root, "pid": os.Getpid()})
	})
	v.mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		list, e := v.store.List(r.URL.Query().Get("refresh") == "1")
		reply(w, list, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}", func(w http.ResponseWriter, r *http.Request) {
		k := r.PathValue("key")
		sum, e := v.store.Find(k)
		if e != nil {
			failure(w, e)
			return
		}
		m, e := v.store.State(k)
		reply(w, map[string]any{"session": sum, "index": m}, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mode := q.Get("mode")
		if mode != "raw" && mode != "json" {
			mode = "visual"
		}
		p, e := v.store.Window(r.PathValue("key"), mode, number(q.Get("from"), 0), number(q.Get("limit"), 100), number(q.Get("turn"), 0), number(q.Get("before"), 0), number(q.Get("after"), 0), q.Get("aux") == "1")
		reply(w, p, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/sources", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, e := v.store.Sources(r.PathValue("key"), number(q.Get("step"), 0), number(q.Get("after"), 0))
		reply(w, p, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/turns", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, e := v.store.Turns(r.PathValue("key"), number(q.Get("from"), 0), number(q.Get("limit"), 30))
		reply(w, p, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/locate", func(w http.ResponseWriter, r *http.Request) {
		line, e := v.store.Locate(r.PathValue("key"), r.URL.Query().Get("time"))
		reply(w, map[string]any{"line": line}, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/segment", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		b, size, e := v.store.Segment(r.PathValue("key"), number(q.Get("offset"), 0), number(q.Get("length"), 65536))
		reply(w, map[string]any{"text": strings.ToValidUTF8(string(b), "�"), "bytes": len(b), "size": size}, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/download", v.download)
	v.mux.HandleFunc("GET /api/session/{key}/node", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		node, e := v.store.Inspect(r.Context(), r.PathValue("key"), number(q.Get("offset"), 0), number(q.Get("length"), 0), number(q.Get("cursor"), 0), number(q.Get("index"), 0), q.Get("kind"))
		reply(w, node, e)
	})
	v.mux.HandleFunc("GET /api/session/{key}/search", v.search)
	v.mux.HandleFunc("GET /api/find", v.search)
	v.mux.HandleFunc("GET /api/projects", v.projects)
	v.mux.HandleFunc("PUT /api/projects", v.saveProjects)
	v.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "app.css" && name != "window.js" {
			http.NotFound(w, r)
			return
		}
		b, e := fs.ReadFile(v.web, name)
		if e != nil {
			failure(w, e)
			return
		}
		if strings.HasSuffix(name, ".js") {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		} else if strings.HasSuffix(name, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.Write(b)
	})
}
func (v *Server) download(w http.ResponseWriter, r *http.Request) {
	sum, e := v.store.Find(r.PathValue("key"))
	if e != nil {
		failure(w, e)
		return
	}
	f, e := os.Open(sum.Path)
	if e != nil {
		failure(w, e)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		failure(w, e)
		return
	}
	offset := max(0, number(r.URL.Query().Get("offset"), 0))
	length := min(st.Size()-offset, number(r.URL.Query().Get("length"), st.Size()))
	if length < 0 {
		http.Error(w, "invalid byte range", 400)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="codex-record.jsonl"`)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	io.Copy(w, io.NewSectionReader(f, offset, length))
}
func (v *Server) search(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query().Get("q")) > 512 {
		http.Error(w, "query too long", 400)
		return
	}
	key := r.PathValue("key")
	if _, e := v.store.Find(key); key != "" && e != nil {
		failure(w, e)
		return
	}
	// Separate streaming budget keeps cancellation and navigation responsive.
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	default:
		http.Error(w, "正在处理其他请求，请稍后重试", 429)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	f, ok := w.(http.Flusher)
	if !ok {
		failure(w, fmt.Errorf("streaming unsupported"))
		return
	}
	enc := json.NewEncoder(w)
	emit := func(u session.SearchUpdate) error { e := enc.Encode(u); f.Flush(); return e }
	search := func() error {
		if key == "" {
			return v.store.SearchAll(r.Context(), r.URL.Query().Get("q"), emit)
		}
		return v.store.Search(r.Context(), key, r.URL.Query().Get("q"), emit)
	}
	if e := search(); e != nil && r.Context().Err() == nil {
		emit(session.SearchUpdate{Done: true, Error: e.Error()})
	}
}
func number(s string, d int64) int64 {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return d
	}
	return n
}
func reply(w http.ResponseWriter, v any, e error) {
	if e != nil {
		failure(w, e)
		return
	}
	write(w, v)
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e := json.NewEncoder(w).Encode(v); e != nil {
		log.Printf("response: %v", e)
	}
}
func failure(w http.ResponseWriter, e error) {
	code := 500
	if os.IsNotExist(e) {
		code = 404
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}

// Project aliases are local preferences, never edits to Codex files.
func (v *Server) projects(w http.ResponseWriter, r *http.Request) {
	b, e := os.ReadFile(v.store.Data + "/projects.json")
	if os.IsNotExist(e) {
		write(w, map[string]string{})
		return
	}
	if e != nil {
		failure(w, e)
		return
	}
	var m map[string]string
	if e = json.Unmarshal(b, &m); e != nil {
		failure(w, e)
		return
	}
	write(w, m)
}
func (v *Server) saveProjects(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var m map[string]string
	if e := json.NewDecoder(r.Body).Decode(&m); e != nil {
		http.Error(w, "invalid preferences", 400)
		return
	}
	for k, s := range m {
		if len(k) > 4096 || len(s) > 256 {
			http.Error(w, "preference too long", 400)
			return
		}
	}
	b, _ := json.Marshal(m)
	f, e := os.CreateTemp(v.store.Data, "projects-*")
	if e != nil {
		failure(w, e)
		return
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(b)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, v.store.Data+"/projects.json")
	}
	reply(w, map[string]bool{"ok": e == nil}, e)
}
