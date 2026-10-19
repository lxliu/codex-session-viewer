package session

import (
	"encoding/xml"
	"io"
	"strings"
)

// Only classify a complete, structurally valid wrapper. Quoted examples and
// messages containing an XML fragment remain ordinary input messages.
func classifyInput(s string, confirmed bool) (source, label, summary string) {
	source, label = "unconfirmed", "输入消息 · 来源未确认"
	if confirmed {
		source, label = "user", "你的输入"
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<environment_context>") && !strings.HasPrefix(s, "<heartbeat>") {
		return
	}
	d := xml.NewDecoder(strings.NewReader(s))
	var root struct {
		XMLName      xml.Name
		Date         string `xml:"current_date"`
		Zone         string `xml:"timezone"`
		CWD          string `xml:"cwd"`
		Automation   string `xml:"automation_id"`
		Time         string `xml:"current_time_iso"`
		Instructions string `xml:"instructions"`
	}
	if d.Decode(&root) != nil {
		return
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return
		}
		if c, ok := t.(xml.CharData); !ok || strings.TrimSpace(string(c)) != "" {
			return
		}
	}
	switch root.XMLName.Local {
	case "environment_context":
		if root.Date == "" && root.Zone == "" && root.CWD == "" {
			return
		}
		source, label = "environment", "运行环境"
		var parts []string
		for _, p := range []string{root.Date, root.Zone, root.CWD} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		summary = strings.Join(parts, " · ") + " · 环境与权限上下文，非用户主动输入"
	case "heartbeat":
		if root.Automation == "" || root.Time == "" || strings.TrimSpace(root.Instructions) == "" {
			return
		}
		source, label = "automation", "定时任务指令"
		// This is an extract from the saved instructions, not an LLM-generated interpretation.
		summary = "定时触发 · " + root.Automation + " · " + cut(strings.Join(strings.Fields(root.Instructions), " "), 180)
	}
	return
}

func inputRow(r Row, confirmed bool) Row {
	r.Identity = key(r.Text)
	r.Source, r.Label, r.Summary = classifyInput(r.Text, confirmed)
	if len(r.Text) > 24000 {
		r.Text = cut(r.Text, 24000)
		r.Truncated = true
	}
	r.Kind = "user"
	if r.Source == "environment" {
		r.Kind = "environment"
	}
	if r.Source == "automation" {
		r.Kind = "automation"
	}
	return r
}

func inputTitle(t *Turn, user string, confirmed bool, family string) {
	source, _, summary := classifyInput(user, confirmed)
	if source == "environment" {
		if t.Source == "" {
			t.Source = source
			t.User = "运行环境更新"
		}
		return
	}
	hash := key(user)
	if t.LastInputHash == hash {
		found := false
		for _, f := range t.InputFamilies {
			if f == family {
				found = true
			}
		}
		if !found {
			t.InputFamilies = append(t.InputFamilies, family)
			if confirmed && t.Source == "unconfirmed" {
				t.Source = "user"
			}
			return
		}
	}
	t.LastInputHash = hash
	t.InputFamilies = []string{family}
	if source == "automation" {
		t.Source = source
		t.User = summary
		t.Inputs++
		return
	}
	if t.Source == "" || t.Source == "environment" || t.Inputs == 0 {
		t.User, t.Source = cut(user, 4000), source
	} else if !strings.Contains(t.User, cut(user, 512)) {
		t.User = cut(t.User+"\n\n↳ 执行中追加："+cut(user, 1800), 8000)
	}
	t.Inputs++
}

// Inputs can contain long injected XML wrappers. Classify the complete text
// within the semantic line limit before creating a bounded display preview.
func inputText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, item := range x {
			s := inputText(item)
			if s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(s)
				if b.Len() > MaxSemantic {
					break
				}
			}
		}
		return b.String()
	case map[string]any:
		for _, k := range []string{"text", "input_text", "output_text", "summary_text", "message", "content", "summary"} {
			if x[k] != nil {
				if s := inputText(x[k]); s != "" {
					return s
				}
			}
		}
	}
	return ""
}
