# JSONL 适配与来源

应用源码在当前项目独立编写，使用 Go 标准库，不链接或运行 Codex Rust 源码，也不读取旁边项目的实现作为运行依赖。

官方研究源码独立保存于 `research/codex/`，版本固定到：

`8c076942cb70fba8e5c4732c23f1faabc4aed484`

源码来源：[openai/codex](https://github.com/openai/codex/tree/8c076942cb70fba8e5c4732c23f1faabc4aed484)。本次 shell 无法解析 `github.com`，直接 clone 两次失败；通过 GitHub 连接获取下面的原始文件。它是独立源码快照，不是完整 Git 仓库。逐文件 blob SHA 记录在 `research/provenance.json`，已按 Git blob 算法校验与该提交的树一致。研究源码受其原始 LICENSE 约束。

| 官方文件 | 核对内容 |
| --- | --- |
| `codex-rs/protocol/src/protocol.rs` | SessionMeta、TokenUsage、TokenUsageRecord、父线程、继承边界、轮次标识 |
| `codex-rs/history/src/lib.rs` | RolloutItem、RolloutLine、事件和响应项 |
| `codex-rs/history/src/rollout_payload.rs` | 序列化类型和 payload 形式 |
| `codex-rs/rollout/src/recorder.rs` | 持久化、ordinal、追加和上下文历史 |
| `codex-rs/rollout/src/policy.rs` | 哪些数据会被保存、哪些数据可能被截断或省略 |
| `codex-rs/rollout/src/lib.rs` | JSONL 入口和解析接口 |

另外只读验证了本机 Codex 0.160.0 / 0.160.1 的真实文件；没有把真实用户内容复制进提交用测试样本。自动测试样本由代码生成。

## 映射

| 输入记录 | 展示 / 处理 |
| --- | --- |
| `session_meta` | 工作目录、会话 ID、来源、模型版本、代理与 fork 关系 |
| `event_msg.task_started` / `turn_context.turn_id` | 轮次边界；缺 ID 时明确采用上下文回退 |
| `item_completed.UserMessage` / `user_message` | 输入消息；进一步识别环境上下文、定时任务，保留同轮后续输入 |
| `AgentMessage` / `response_item.message` | Codex 进展与最终答复 |
| `Reasoning` / `response_item.reasoning` | 已保存的可读摘要；不解密隐藏内容 |
| `CommandExecution`、旧命令 begin/end | 命令、输出、退出码、显式耗时 |
| `FileChange`、patch 记录 | 已保存的修改与差异 |
| 工具调用 / 返回、MCP、Extension、WebSearch | 参数、结果和原始证据 |
| `token_usage_record` | 请求用量、轮次累计、线程累计；与旧快照区分 |
| `event_msg.token_count` | 旧格式兼容累计，不作为额外消耗重复相加 |
| `compacted` / `ContextCompaction` | 压缩里程碑与实际记录 |
| 代理通信和协作调用 | 任务分配、消息与等待 |
| 中断、错误、警告 | 显示实际状态，避免标为成功 |
| 未知类型、普通 JSON 值、损坏行 | 保留原始范围和通用 JSON 查看入口 |

可视化在磁盘中建立步骤投影，按调用 ID、消息 ID 和不同消息表示的完整内容指纹关联来源。同一种表示里重复出现的相同文字不会仅按内容去重。调用和返回可跨加载窗口关联，每个新步骤只分配一次全局编号；追加和缓存重开均保留编号。关联历史最多 2048 项，超出范围且缺少可用关联时保留为独立步骤，不猜测合并。源记录索引 48 字节，投影引用 24 字节/步骤，关联链接 8 字节/源行；不在索引中复制工具输出。索引版本升级会自动重建旧缓存。

`role=user` 只表示模型输入角色，不保证由人主动输入。完整且有效的 `environment_context` / `heartbeat` 包装按结构识别；普通正文中的 XML 片段不触发此规则。定时任务默认折叠并提取原始指令摘要，环境上下文不增加人工输入计数；无法确认来源的消息明确标注。完整输入在有界语义解析范围内先分类，再截取显示预览，长指令仍有原文入口。

三种模式使用排他性的 `before` / `after` 游标。原始与 JSONL 模式游标为物理行，可视化为步骤号；日期和搜索跳转始终用物理行，由后端转换。反向加载从边界向前读取再反转，避免 2MB 字节预算造成漏行。前端保留有界记录窗口，并按可见记录锚点恢复位置；保留未变化的 DOM 节点以维持展开状态。

累计 token 使用线程累计快照的分量最大值，兼容记录重复 / 重放；没有累计字段时回退到去重的请求用量。该策略面向当前已验证日志，不承诺任意未来格式都能准确计费。若不同版本改变累计字段含义，应新增适配并提高解析器版本，让旧缓存自动重建。
