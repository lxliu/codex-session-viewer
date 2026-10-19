package session

import (
	"fmt"
	"sort"
	"strings"
)

// Normalize only describes facts present in the record. It never invents model
// reasoning, token attribution to tools, or durations between unrelated events.
func Normalize(m map[string]any) Row {
	r := Row{Kind: "system", Label: "系统记录", Timestamp: str(m["timestamp"])}
	p := obj(m["payload"])
	typ := str(m["type"])
	sub := str(p["type"])
	r.ID = first(p, "id", "call_id")
	r.Call = str(p["call_id"])
	switch typ {
	case "session_meta":
		r.Label = "会话信息"
		r.Text = pretty(p)
	case "turn_context":
		r.Label = "本轮模型与执行环境"
		r.Text = pretty(p)
	case "world_state":
		r.Label = "运行环境更新"
		r.Text = pretty(p)
	case "token_usage_record":
		u := usage(obj(p["usage"]))
		r.Kind = "token"
		r.Label = "一次模型请求的用量"
		r.Usage = &u
		r.Text = fmt.Sprintf("本次输入 %d token，其中缓存命中 %d；输出 %d，其中推理输出 %d。\n\n输入包含缓存命中；输出包含推理输出，不能重复相加。工具返回内容会影响后续模型请求，不能据此精确分摊。", u.Input, u.Cached, u.Output, u.Reasoning)
		r.ID = str(p["response_id"])
	case "compacted":
		r.Kind = "compact"
		r.Label = "压缩上下文，继续长任务"
		r.Text = text(p["message"])
		if r.Text == "" {
			r.Text = "Codex 将较早上下文压缩为摘要。原始日志仍按实际记录展示。"
		}
	case "inter_agent_communication":
		r.Kind = "agent"
		r.Label = "代理间通信"
		r.Text = pretty(p)
	case "retained_context":
		r.Kind = "context"
		r.Label = "保留的上下文"
		r.Text = pretty(p)
	case "response_item":
		switch sub {
		case "message":
			role := str(p["role"])
			r.Text = text(p["content"])
			if role == "user" {
				r.Text = inputText(p["content"])
				r.Kind = "user"
				r.Label = "输入消息 · 来源未确认"
			} else if role == "assistant" {
				r.Kind = "message"
				r.Label = "Codex 回复"
				if str(p["phase"]) == "final_answer" {
					r.Label = "任务答复"
				}
			} else {
				r.Label = "提供给模型的指令"
			}
		case "reasoning":
			r.Kind = "reasoning"
			r.Label = "分析下一步"
			r.Text = text(p["summary"])
			if r.Text == "" {
				r.Text = "日志未提供可读的推理摘要；加密内容无法还原。"
			}
		case "function_call", "custom_tool_call":
			r.Kind = "tool"
			r.Label = "调用工具 · " + first(p, "name")
			r.Text = first(p, "arguments", "input")
			name := str(p["name"])
			ns := str(p["namespace"])
			switch {
			case strings.Contains(name, "spawn_agent") || strings.Contains(ns, "collaboration") || strings.Contains(name, "send_message") || strings.Contains(name, "wait_agent"):
				r.Kind = "agent"
				r.Label = "协作与任务分配 · " + name
			case strings.Contains(name, "request_user_input"):
				r.Kind = "question"
				r.Label = "向你确认问题"
			case strings.Contains(name, "exec_command"):
				r.Kind = "command"
				r.Label = "执行命令"
			case strings.Contains(name, "apply_patch"):
				r.Kind = "file"
				r.Label = "修改文件"
			case name == "sleep" || name == "wait":
				r.Kind = "wait"
				r.Label = "等待结果"
			case strings.Contains(name, "web") || strings.Contains(ns, "web"):
				r.Kind = "web"
				r.Label = "检索或浏览网页"
			}
			r.Text = cut(r.Text, 24000)
		case "function_call_output", "custom_tool_call_output":
			r.Kind = "result"
			r.Label = "工具返回结果"
			r.Text = text(p["output"])
			if r.Text == "" {
				r.Text = pretty(p["output"])
			}
		case "agent_message":
			r.Kind = "agent"
			r.Label = "收到其他代理的消息"
			r.Text = text(p["content"])
		case "compaction", "compaction_trigger", "context_compaction":
			r.Kind = "compact"
			r.Label = "上下文压缩记录"
			r.Text = "压缩状态或加密结果；点击原始记录检查。"
		case "web_search_call":
			r.Kind = "web"
			r.Label = "搜索网页"
			r.Text = pretty(p["action"])
		default:
			r.Kind = "unknown"
			r.Label = "尚未适配的记录 · " + sub
			r.Text = pretty(p)
		}
	case "event_msg":
		switch sub {
		case "item_completed":
			return normalizeItem(p, r)
		case "task_started", "turn_started":
			r.Label = "开始处理这轮任务"
			r.Text = "轮次 ID：" + str(p["turn_id"])
		case "task_complete", "turn_complete":
			r.Kind = "finish"
			r.Label = "本轮执行完成"
			r.Text = text(p["last_agent_message"])
			if r.Text == "" {
				r.Text = "本轮已完成。上方保留了执行步骤与可用的任务答复。"
			}
			r.Duration = num(p["duration_ms"])
			r.Start = str(p["started_at"])
			r.End = str(p["completed_at"])
		case "turn_aborted":
			r.Kind = "error"
			r.Label = "执行被中断"
			r.Text = first(p, "reason", "message")
			r.Duration = num(p["duration_ms"])
		case "error", "stream_error", "warning":
			r.Kind = "error"
			r.Label = "执行异常 / 警告"
			r.Text = pretty(p)
		case "user_message":
			r.Kind = "user"
			r.Label = "输入消息 · 来源未确认"
			r.Text = inputText(p["message"])
		case "agent_message":
			r.Kind = "message"
			r.Label = "Codex 回复"
			r.Text = text(p["message"])
		case "agent_reasoning", "agent_reasoning_raw_content":
			r.Kind = "reasoning"
			r.Label = "分析下一步"
			r.Text = text(p["text"])
		case "token_count":
			r.Kind = "token"
			r.Label = "用量累计快照"
			u := usage(obj(obj(p["info"])["total_token_usage"]))
			r.Usage = &u
			r.Text = "这是累计快照，不能与单次请求记录相加。"
		case "exec_command_begin", "exec_command_end":
			r.Kind = "command"
			r.Label = "执行命令"
			r.Call = str(p["call_id"])
			r.Text = pretty(p["command"])
			if o := first(p, "aggregated_output", "stdout"); o != "" {
				r.Text += "\n\n" + cut(o, 20000)
			}
			r.Duration = duration(p)
			if sub == "exec_command_end" {
				r.Status = fmt.Sprintf("退出码 %d", num(p["exit_code"]))
			}
		case "patch_apply_begin", "patch_apply_end":
			r.Kind = "file"
			r.Label = "应用文件修改"
			r.Text = changeText(p["changes"])
			r.Call = str(p["call_id"])
		case "mcp_tool_call_begin", "mcp_tool_call_end":
			r.Kind = "tool"
			r.Label = "调用外部工具"
			r.Call = str(p["call_id"])
			r.Text = pretty(p)
		case "plan_update":
			r.Kind = "plan"
			r.Label = "更新执行计划"
			r.Text = pretty(p["plan"])
		case "request_user_input":
			r.Kind = "question"
			r.Label = "向你确认问题"
			r.Text = pretty(p)
		default:
			if strings.Contains(sub, "collab") || strings.Contains(sub, "sub_agent") {
				r.Kind = "agent"
				r.Label = "多代理协作 · " + sub
			} else {
				r.Label = "系统事件 · " + sub
			}
			r.Text = pretty(p)
		}
	default:
		r.Kind = "unknown"
		r.Label = "尚未适配的记录 · " + typ
		r.Text = pretty(p)
	}
	if r.Kind == "user" {
		r = inputRow(r, typ == "event_msg")
	}
	if r.Text == "" {
		r.Text = "此记录没有可读文字，可展开原始 JSON 查看字段。"
	}
	return r
}
func duration(p map[string]any) int64 {
	if n := num(p["duration_ms"]); n > 0 {
		return n
	}
	if n := num(p["durationMs"]); n > 0 {
		return n
	}
	d := obj(p["duration"])
	return num(d["secs"])*1000 + num(d["nanos"])/1000000
}
func normalizeItem(p map[string]any, r Row) Row {
	i := obj(p["item"])
	typ := str(i["type"])
	r.ID = str(i["id"])
	r.Call = r.ID
	r.Start = iso(num(p["started_at_ms"]))
	r.End = iso(num(p["completed_at_ms"]))
	r.Duration = max(0, num(p["completed_at_ms"])-num(p["started_at_ms"]))
	r.Status = str(i["status"])
	switch typ {
	case "UserMessage":
		r.Kind = "user"
		r.Label = "输入消息 · 来源未确认"
		r.Text = inputText(i["content"])
	case "AgentMessage":
		r.Kind = "message"
		r.Label = "Codex 回复"
		r.Text = text(i["content"])
		if str(i["phase"]) == "final_answer" {
			r.Label = "任务答复"
		}
	case "Reasoning":
		r.Kind = "reasoning"
		r.Label = "分析下一步"
		r.Text = first(i, "summary_text", "raw_content")
		if r.Text == "" {
			r.Text = "日志未提供可读推理内容。"
		}
	case "CommandExecution":
		r.Kind = "command"
		r.Label = "执行命令"
		r.Text = pretty(i["command"])
		if o := first(i, "aggregated_output", "stdout", "formatted_output"); o != "" {
			r.Text += "\n\n" + cut(o, 20000)
		}
		if d := duration(i); d > 0 {
			r.Duration = d
		}
		if code, ok := i["exit_code"]; ok {
			r.Status = fmt.Sprintf("退出码 %d", num(code))
		}
	case "FileChange":
		r.Kind = "file"
		r.Label = "修改文件"
		r.Text = changeText(i["changes"])
	case "McpToolCall", "DynamicToolCall":
		r.Kind = "tool"
		r.Label = "调用工具 · " + first(i, "tool", "name")
		r.Text = "参数\n" + pretty(i["arguments"]) + "\n\n结果\n" + pretty(i["result"])
		if d := duration(i); d > 0 {
			r.Duration = d
		}
	case "Plan":
		r.Kind = "plan"
		r.Label = "执行计划"
		r.Text = text(i["text"])
	case "ContextCompaction":
		r.Kind = "compact"
		r.Label = "压缩上下文"
		r.Text = "Codex 正在缩短历史上下文，以便继续执行长任务。"
	case "CollabAgentToolCall", "CollabToolCall":
		r.Kind = "agent"
		r.Label = "代理协作"
		r.Text = pretty(i)
	case "WebSearch":
		r.Kind = "web"
		r.Label = "搜索或浏览网页"
		r.Text = pretty(i)
	case "ImageView", "ImageGeneration":
		r.Kind = "image"
		r.Label = "查看或生成图片"
		r.Text = pretty(i)
	case "Extension":
		r.Kind = "tool"
		r.Label = "扩展工具 · " + first(i, "kind")
		r.Text = pretty(i)
	default:
		r.Kind = "unknown"
		r.Label = "执行步骤 · " + typ
		r.Text = pretty(i)
	}
	if typ == "UserMessage" {
		r = inputRow(r, true)
	}
	r.Text = cut(r.Text, 24000)
	return r
}

func changeText(v any) string {
	var parts []string
	format := func(path string, m map[string]any) {
		if path == "" {
			path = first(m, "path", "file")
		}
		kind := first(m, "kind", "type")
		if k := obj(m["kind"]); k != nil {
			kind = first(k, "type")
		}
		diff := first(m, "diff", "unified_diff", "patch")
		if diff == "" {
			diff = pretty(m)
		}
		parts = append(parts, "文件："+path+"\n操作："+kind+"\n"+cut(diff, 20000))
	}
	if m := obj(v); m != nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			format(k, obj(m[k]))
			if len(parts) > 40 {
				break
			}
		}
	} else {
		for _, x := range arr(v) {
			format("", obj(x))
			if len(parts) > 40 {
				break
			}
		}
	}
	if len(parts) == 0 {
		return pretty(v)
	}
	return cut(strings.Join(parts, "\n\n"), 24000)
}
