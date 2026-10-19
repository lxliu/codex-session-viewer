package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const Version = 5
const RecordSize = 48
const MaxSemantic = 8 << 20
const PageBudget = 2 << 20

type Usage struct {
	Input      int64 `json:"input"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cacheWrite"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}

func (u Usage) Add(v Usage) Usage {
	return Usage{u.Input + v.Input, u.Cached + v.Cached, u.CacheWrite + v.CacheWrite, u.Output + v.Output, u.Reasoning + v.Reasoning, u.Total + v.Total}
}
func (u Usage) Max(v Usage) Usage {
	return Usage{max(u.Input, v.Input), max(u.Cached, v.Cached), max(u.CacheWrite, v.CacheWrite), max(u.Output, v.Output), max(u.Reasoning, v.Reasoning), max(u.Total, v.Total)}
}
func (u Usage) Delta(v Usage) Usage {
	return Usage{max(0, u.Input-v.Input), max(0, u.Cached-v.Cached), max(0, u.CacheWrite-v.CacheWrite), max(0, u.Output-v.Output), max(0, u.Reasoning-v.Reasoning), max(0, u.Total-v.Total)}
}
func usage(m map[string]any) Usage {
	return Usage{num(m["input_tokens"]), num(m["cached_input_tokens"]), num(m["cache_write_input_tokens"]), num(m["output_tokens"]), num(m["reasoning_output_tokens"]), num(m["total_tokens"])}
}

type Summary struct {
	Key          string `json:"key"`
	ID           string `json:"id"`
	SessionID    string `json:"sessionId,omitempty"`
	Parent       string `json:"parent,omitempty"`
	Fork         string `json:"fork,omitempty"`
	Path         string `json:"path"`
	CWD          string `json:"cwd"`
	Title        string `json:"title"`
	Model        string `json:"model,omitempty"`
	Role         string `json:"role,omitempty"`
	Agent        string `json:"agent,omitempty"`
	CLI          string `json:"cli,omitempty"`
	Source       any    `json:"source,omitempty"`
	HistoryBase  any    `json:"historyBase,omitempty"`
	HistoryStart int64  `json:"historyStart,omitempty"`
	Size         int64  `json:"size"`
	Modified     int64  `json:"modified"`
	Start        string `json:"start,omitempty"`
	Archived     bool   `json:"archived"`
}
type Turn struct {
	LastInputHash    string   `json:"lastInputHash,omitempty"`
	InputFamilies    []string `json:"inputFamilies,omitempty"`
	Source           string   `json:"source,omitempty"`
	Number           int64    `json:"number"`
	ID               string   `json:"id"`
	Root             string   `json:"root,omitempty"`
	First            int64    `json:"first"`
	Last             int64    `json:"last"`
	Start            string   `json:"start,omitempty"`
	End              string   `json:"end,omitempty"`
	User             string   `json:"user"`
	Inputs           int      `json:"inputs"`
	Status           string   `json:"status"`
	Usage            Usage    `json:"usage"`
	Known            bool     `json:"known"`
	Requests         int64    `json:"requests"`
	Duration         int64    `json:"duration"`
	ExplicitDuration bool     `json:"explicitDuration"`
	ModernUsage      bool     `json:"modernUsage"`
	RecentResponses  []string `json:"recentResponses,omitempty"`
}
type Stats struct {
	Usage       Usage  `json:"usage"`
	Known       bool   `json:"known"`
	Requests    int64  `json:"requests"`
	ToolCalls   int64  `json:"toolCalls"`
	Compactions int64  `json:"compactions"`
	Invalid     int64  `json:"invalid"`
	Large       int64  `json:"large"`
	Unassigned  int64  `json:"unassigned"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
}
type Checkpoint struct {
	Line   int64 `json:"line"`
	Time   int64 `json:"time"`
	Offset int64 `json:"offset"`
}
type Manifest struct {
	Steps       int64            `json:"steps"`
	Projection  map[string]int64 `json:"-"`
	Version     int              `json:"version"`
	Path        string           `json:"path"`
	Generation  string           `json:"generation"`
	Size        int64            `json:"size"`
	Modified    int64            `json:"modified"`
	Prefix      string           `json:"prefix"`
	Boundary    string           `json:"boundary"`
	Offset      int64            `json:"offset"`
	Lines       int64            `json:"lines"`
	Turns       int64            `json:"turns"`
	Current     *Turn            `json:"current,omitempty"`
	Stats       Stats            `json:"stats"`
	NewUsage    Usage            `json:"newUsage"`
	OldUsage    Usage            `json:"oldUsage"`
	NewKnown    bool             `json:"newKnown"`
	OldKnown    bool             `json:"oldKnown"`
	NoNewline   bool             `json:"noNewline"`
	Checkpoints []Checkpoint     `json:"checkpoints"`
	Monotonic   bool             `json:"monotonic"`
	LastTime    int64            `json:"lastTime"`
	Inherited   int64            `json:"inherited"`
	Complete    bool             `json:"complete"`
	Running     bool             `json:"running"`
	Error       string           `json:"error,omitempty"`
}
type cachedManifest struct {
	Manifest
	Projection map[string]int64 `json:"projection,omitempty"`
}

type Ref struct{ Offset, Size, Timestamp, Turn, Flags, Visual int64 }
type Evidence struct {
	Line   int64 `json:"line"`
	Offset int64 `json:"offset"`
	Size   int64 `json:"size"`
}
type Row struct {
	Identity    string     `json:"-"`
	Number      int64      `json:"number,omitempty"`
	Source      string     `json:"source,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	Sources     []Evidence `json:"sources,omitempty"`
	SourceCount int64      `json:"sourceCount,omitempty"`
	Line        int64      `json:"line"`
	Offset      int64      `json:"offset"`
	Size        int64      `json:"size"`
	Turn        int64      `json:"turn"`
	Timestamp   string     `json:"timestamp,omitempty"`
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	Text        string     `json:"text,omitempty"`
	Raw         string     `json:"raw,omitempty"`
	JSON        any        `json:"json,omitempty"`
	Truncated   bool       `json:"truncated"`
	ID          string     `json:"id,omitempty"`
	Call        string     `json:"call,omitempty"`
	Duration    int64      `json:"duration,omitempty"`
	Start       string     `json:"start,omitempty"`
	End         string     `json:"end,omitempty"`
	Status      string     `json:"status,omitempty"`
	Usage       *Usage     `json:"usage,omitempty"`
}
type Page struct {
	Before    int64    `json:"before"`
	After     int64    `json:"after"`
	HasBefore bool     `json:"hasBefore"`
	HasAfter  bool     `json:"hasAfter"`
	Rows      []Row    `json:"rows"`
	First     int64    `json:"first"`
	Next      int64    `json:"next"`
	Total     int64    `json:"total"`
	Tail      bool     `json:"tail"`
	Index     Manifest `json:"index"`
}

func key(s string) string      { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:12]) }
func str(v any) string         { s, _ := v.(string); return s }
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case int64:
		return n
	}
	return 0
}
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, i := range x {
			s := text(i)
			if s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(cut(s, 12000))
				if b.Len() > 24000 {
					break
				}
			}
		}
		return b.String()
	case map[string]any:
		for _, k := range []string{"text", "input_text", "output_text", "message", "content", "summary_text", "summary"} {
			if x[k] != nil {
				s := text(x[k])
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}
func stamp(s string) int64 {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return 0
	}
	return t.UnixMilli()
}
func iso(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}
func pretty(v any) string {
	if s, ok := v.(string); ok {
		return cut(s, 24000)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return cut(string(b), 24000)
}
func first(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m[k]); s != "" {
			return s
		}
	}
	return ""
}
func decode(b []byte) (map[string]any, error) {
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	err := d.Decode(&m)
	if err == nil && m == nil {
		err = fmt.Errorf("record is not an object")
	}
	if err == nil {
		var extra any
		if err2 := d.Decode(&extra); err2 != io.EOF {
			err = fmt.Errorf("multiple JSON values")
		}
	}
	return m, err
}

func decodeValue(b []byte) (any, error) {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("multiple JSON values")
	}
	return v, nil
}
func integer(s string, def int64) int64 {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return def
	}
	return n
}
