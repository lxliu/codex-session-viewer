package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// The JSON inspector scans ranges instead of unmarshalling whole strings/arrays.
// Each child is a byte range in the original file, so even a 5GB scalar is bounded.
type NodeChild struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Offset  int64  `json:"offset"`
	Size    int64  `json:"size"`
	Preview string `json:"preview,omitempty"`
}
type Node struct {
	Kind     string      `json:"kind"`
	Children []NodeChild `json:"children"`
	Next     int64       `json:"next"`
	Index    int64       `json:"index"`
	Preview  string      `json:"preview,omitempty"`
	Offset   int64       `json:"offset"`
	Size     int64       `json:"size"`
}
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(b)
	}
}

type jsonScan struct {
	r     *bufio.Reader
	pos   int64
	depth int
}

func (j *jsonScan) peek() (byte, error) {
	b, e := j.r.Peek(1)
	if e != nil {
		return 0, e
	}
	return b[0], nil
}
func (j *jsonScan) take() (byte, error) {
	b, e := j.r.ReadByte()
	if e == nil {
		j.pos++
	}
	return b, e
}
func (j *jsonScan) space() error {
	for {
		c, e := j.peek()
		if e != nil {
			return e
		}
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return nil
		}
		j.take()
	}
}
func (j *jsonScan) stringValue() (string, error) {
	c, e := j.take()
	if e != nil || c != '"' {
		return "", fmt.Errorf("expected JSON string")
	}
	capture := make([]byte, 0, 1024)
	for {
		if _, e = j.r.Peek(1); e != nil {
			return "", e
		}
		available := j.r.Buffered()
		part, _ := j.r.Peek(available)
		idx := bytes.IndexAny(part, "\"\\")
		if idx < 0 {
			idx = len(part)
		}
		if idx > 0 {
			n := min(idx, 1024-len(capture))
			if n > 0 {
				capture = append(capture, part[:n]...)
			}
			j.r.Discard(idx)
			j.pos += int64(idx)
		}
		if idx == len(part) {
			continue
		}
		c, e = j.take()
		if e != nil {
			return "", e
		}
		if c == '"' {
			break
		}
		if len(capture) < 1024 {
			capture = append(capture, c)
		}
		c, e = j.take()
		if e != nil {
			return "", e
		}
		if len(capture) < 1024 {
			capture = append(capture, c)
		}
	}
	for n := len(capture); n >= max(0, len(capture)-8); n-- {
		if s, e := strconv.Unquote("\"" + string(capture[:n]) + "\""); e == nil {
			return s, nil
		}
	}
	return string(capture), nil
}
func (j *jsonScan) value() (kind, preview string, err error) {
	if err = j.space(); err != nil {
		return
	}
	c, e := j.peek()
	if e != nil {
		return "", "", e
	}
	switch c {
	case '"':
		s, e := j.stringValue()
		return "string", s, e
	case '{', '[':
		kind = "object"
		if c == '[' {
			kind = "array"
		}
		j.take()
		depth := 1
		for depth > 0 {
			if _, e = j.r.Peek(1); e != nil {
				return "", "", e
			}
			part, _ := j.r.Peek(j.r.Buffered())
			i := bytes.IndexAny(part, "\"{}[]")
			if i < 0 {
				j.r.Discard(len(part))
				j.pos += int64(len(part))
				continue
			}
			if i > 0 {
				j.r.Discard(i)
				j.pos += int64(i)
			}
			c, _ = j.peek()
			if c == '"' {
				if _, e = j.stringValue(); e != nil {
					return "", "", e
				}
				continue
			}
			j.take()
			if c == '{' || c == '[' {
				depth++
				if depth > 10000 {
					return "", "", fmt.Errorf("JSON nesting too deep")
				}
			} else {
				depth--
			}
		}
		return kind, "", nil
	default:
		raw := make([]byte, 0, 128)
		for {
			c, e = j.peek()
			if e == io.EOF {
				break
			}
			if e != nil {
				return "", "", e
			}
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\n' || c == '\r' || c == '\t' {
				break
			}
			j.take()
			if len(raw) < 1024 {
				raw = append(raw, c)
			} else {
				return "", "", fmt.Errorf("invalid scalar")
			}
		}
		if len(raw) == 0 {
			return "", "", fmt.Errorf("missing JSON value")
		}
		kind = "number"
		if string(raw) == "true" || string(raw) == "false" {
			kind = "boolean"
		} else if string(raw) == "null" {
			kind = "null"
		} else if !json.Valid(raw) {
			return "", "", fmt.Errorf("invalid JSON scalar")
		}
		return kind, string(raw), nil
	}
}
func (s *Store) Inspect(ctx context.Context, k string, offset, length, cursor, index int64, kind string) (Node, error) {
	sum, e := s.Find(k)
	if e != nil {
		return Node{}, e
	}
	f, e := os.Open(sum.Path)
	if e != nil {
		return Node{}, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return Node{}, e
	}
	if offset < 0 || length <= 0 || offset > st.Size() || length > st.Size()-offset {
		return Node{}, fmt.Errorf("invalid JSON byte range")
	}
	start := offset
	if cursor > offset && cursor < offset+length {
		start = cursor
	}
	scan := &jsonScan{r: bufio.NewReaderSize(contextReader{ctx, io.NewSectionReader(f, start, offset+length-start)}, 64<<10), pos: start}
	node := Node{Offset: offset, Size: length, Children: []NodeChild{}, Index: index}
	if start == offset {
		if e = scan.space(); e != nil {
			return node, e
		}
		c, e := scan.peek()
		if e != nil {
			return node, e
		}
		if c == '{' {
			kind = "object"
			scan.take()
		} else if c == '[' {
			kind = "array"
			scan.take()
		} else {
			node.Kind, node.Preview, e = scan.value()
			return node, e
		}
	}
	if kind != "object" && kind != "array" {
		return node, fmt.Errorf("invalid JSON container")
	}
	node.Kind = kind
	for len(node.Children) < 80 {
		if e = scan.space(); e != nil {
			return node, e
		}
		c, e := scan.peek()
		if e != nil {
			return node, e
		}
		if c == '}' || c == ']' {
			scan.take()
			return node, nil
		}
		if c == ',' {
			scan.take()
			if e = scan.space(); e != nil {
				return node, e
			}
		}
		label := fmt.Sprint(node.Index)
		if kind == "object" {
			label, e = scan.stringValue()
			if e != nil {
				return node, e
			}
			if e = scan.space(); e != nil {
				return node, e
			}
			c, e = scan.take()
			if e != nil || c != ':' {
				return node, fmt.Errorf("missing colon")
			}
			if e = scan.space(); e != nil {
				return node, e
			}
		}
		child := NodeChild{Key: label, Offset: scan.pos}
		child.Kind, child.Preview, e = scan.value()
		if e != nil {
			return node, e
		}
		child.Size = scan.pos - child.Offset
		node.Children = append(node.Children, child)
		node.Index++
	}
	node.Next = scan.pos
	return node, nil
}
