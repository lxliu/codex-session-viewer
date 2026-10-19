# codex-session-viewer

通过本机 Web 页面查看 Codex 会话，帮助用户看清：**Codex 做了哪些事、哪次输入触发了这些动作、token 和时间花在哪里、多个 agent 如何协作。**

本页面向帮助用户安装、启动和使用本工具的 agent。默认使用已编译的二进制，**不需要安装 Go、Node、Python、数据库或前端依赖**。开发本系统的 agent 请阅读 [AGENTS.md](AGENTS.md)。

## 给使用者的 agent

1. 确认用户的操作系统、CPU 架构和 Codex 会话目录，选择对应二进制。
2. 下载并启动本机服务，检查健康接口成功后，把浏览器地址交给用户。
3. 引导用户先看“执行过程”，再按需要查看 token、时间、代理关系和原始证据。
4. 遇到缺少记录、索引未完成或来源不明确时，说明实际状态，不编造执行过程或消耗归因。

## 下载即用

### 方式一：下载仓库，使用后台管理脚本（macOS / Linux）

```sh
git clone https://github.com/lxliu/codex-session-viewer.git
cd codex-session-viewer
```

仓库的 `bin/` 已包含六个平台的二进制。先把本机对应的文件复制到脚本使用的固定路径：

```sh
viewer_os=$(uname -s)
case "$viewer_os" in
  Darwin) viewer_os=darwin ;;
  Linux) viewer_os=linux ;;
  *) echo "此脚本示例仅适用于 macOS / Linux" >&2; exit 1 ;;
esac
viewer_arch=$(uname -m)
case "$viewer_arch" in
  arm64|aarch64) viewer_arch=arm64 ;;
  x86_64|amd64) viewer_arch=amd64 ;;
  *) echo "暂未提供此 CPU 架构的二进制" >&2; exit 1 ;;
esac
cp "bin/codex-session-viewer-$viewer_os-$viewer_arch" bin/codex-session-viewer
chmod +x bin/codex-session-viewer
./restart.sh
./status.sh
```

然后访问 **http://127.0.0.1:10002**。停止服务：

```sh
./stop.sh
```

`restart.sh`、`status.sh`、`stop.sh` 不需要 Go；使用系统中的 `sh`、`curl`、`ps` 等工具。只有缺少 `bin/codex-session-viewer` 时，启动脚本才会尝试编译，因此下载后应先完成上面的平台选择。

### 方式二：只下载一个二进制

下面的链接直接下载 `main` 分支中的文件；无需下载整个仓库。

| 系统 | CPU | 下载 |
| --- | --- | --- |
| macOS | Apple Silicon / ARM64 | [codex-session-viewer-darwin-arm64](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-darwin-arm64) |
| macOS | Intel / x86-64 | [codex-session-viewer-darwin-amd64](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-darwin-amd64) |
| Linux | ARM64 / aarch64 | [codex-session-viewer-linux-arm64](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-linux-arm64) |
| Linux | x86-64 / amd64 | [codex-session-viewer-linux-amd64](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-linux-amd64) |
| Windows | x86-64 / AMD64 | [codex-session-viewer-windows-amd64.exe](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-windows-amd64.exe) |
| Windows | ARM64 | [codex-session-viewer-windows-arm64.exe](https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-windows-arm64.exe) |

例如，在 macOS Apple Silicon 上：

```sh
mkdir -p codex-session-viewer
cd codex-session-viewer
curl -fL https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-darwin-arm64 -o codex-session-viewer
chmod +x codex-session-viewer
./codex-session-viewer --port 10002
```

Windows x86-64 的 PowerShell 示例：

```powershell
New-Item -ItemType Directory -Force codex-session-viewer | Out-Null
Set-Location codex-session-viewer
Invoke-WebRequest -Uri 'https://raw.githubusercontent.com/lxliu/codex-session-viewer/main/bin/codex-session-viewer-windows-amd64.exe' -OutFile 'codex-session-viewer.exe'
.\codex-session-viewer.exe --port 10002
```

其他平台选择表格中对应的文件即可。以上命令以前台方式运行，保持终端打开，按 `Ctrl+C` 停止。服务启动后打开 **http://127.0.0.1:10002**。

二进制已内嵌全部网页资源，可单独复制到其他目录运行。首次运行会自动创建缓存和日志目录。`bin/codex-session-viewer` 是构建机器的平台产物，跨机器使用时请明确选择带系统和架构后缀的文件。

## 会话目录、端口与健康检查

默认读取 `CODEX_HOME`；未设置时读取当前用户的 `~/.codex`。递归发现其中所有 `.jsonl`，包括历史归档和子代理会话。工具对这些文件只读。

指定其他 Codex 根目录或空闲端口：

```sh
# 下载仓库后使用脚本
CODEX_HOME=/path/to/.codex PORT=10102 ./restart.sh

# 单二进制运行
./codex-session-viewer --codex-home /path/to/.codex --port 10102
```

脚本方式也支持 `PORT=0 ./restart.sh`，启动成功时打印实际地址。服务仅监听本机，不需要登录。

agent 应验证健康接口，再告知用户服务可用：

```sh
curl --noproxy '*' -fsS http://127.0.0.1:10002/api/health
```

返回 JSON 中应包含 `"ok": true`。修改端口后，健康检查和浏览器地址都要使用实际端口。

| 参数 | 作用 |
| --- | --- |
| `--codex-home <目录>` | 指定需要浏览的 Codex 根目录 |
| `--port <端口>` | 默认 `10002`；`0` 自动选择空闲端口 |
| `--data-dir <目录>` | 指定缓存及项目分组偏好的保存目录 |
| `--version` | 查看二进制版本 |
| `--help` | 查看完整参数 |

## 引导用户使用

### 1. 找到会话

左侧先选项目，展开后选择会话。筛选框匹配项目、会话标题、目录、模型和代理等信息。项目菜单支持改名；多个目录使用相同项目名可以合并分组。

如果关键词只存在于会话正文，使用“在所有会话内容中查找”；进入会话后，也可用全文搜索查找该文件中的输入、命令和工具返回内容。

### 2. 默认看“执行过程”

按执行轮次查看输入、分析摘要、计划、命令、修改文件、工具返回、答复和异常。展开步骤查看细节，通过原始记录入口核对证据。

- `#1、#2…` 是全局可视化步骤编号；一个步骤可能合并多条源记录。
- `L246` 是 JSONL 物理行号，与步骤编号含义不同。
- “运行环境”和“定时任务指令”默认收起，保留摘要和原文；它们不等于用户主动输入。
- 来源无法确认的消息会明确标注。

### 3. 看 token 和时间花在哪里

先看会话概览，再选择具体执行轮次，并展开模型请求或耗时步骤。

- 输入包含缓存命中，输出包含推理输出，不要重复相加。
- 累计 token 是模型处理量，不等于用户输入字数，也不等于实际账单。
- 工具输出会影响后续请求，但日志通常不能把 token 精确分摊给每条命令。
- 会话总历时包含用户间隔和等待；步骤耗时只采用日志明确记录的数据。
- 多 agent 和并行工具的耗时可能重叠，不应直接相加。
- 缺少用量或时间记录时显示“未记录”，不按零推断。

代理协作视图按父子线程展示关系、记录时间区间和各线程已知消耗，可进入子代理会话查看细节。

### 4. 核对 JSONL 或原始内容

切换至“JSONL”可按字段、对象和数组展开；切换至“原始内容”可按源行查看文本。长字段按需读取，超长记录可以分段查看或下载完整原文。

三种模式都支持上下滚动自动加载，默认优先显示最新内容。向上阅读会暂停实时跟随，点击“回到最新”或恢复“实时跟随”继续追踪。

### 5. 查看大文件与历史时间

支持超过 5GB 的单个 JSONL 文件。首次打开会先显示最新内容，同时在后台建立磁盘索引。索引期间的统计尚不完整，步骤显示“编号计算中”；完成后补齐全局编号，并可通过日期时间或执行轮次定位。

索引缓存会加速后续访问；前端只保留有界的阅读窗口，不会把整个文件一次性载入浏览器。具体实测条件和结果见 [测试报告](docs/testing.md)。

## 文件保存在哪里

| 路径 | 用途 |
| --- | --- |
| `bin/` | 可直接运行的各平台二进制，已纳入仓库 |
| `data/indexes/` | 自动生成、可重建的磁盘索引 |
| `data/projects.json` | 项目改名与合并偏好 |
| `data/server.pid`、`data/server.port` | 后台脚本记录的进程与端口 |
| `log/server.log` | 运行日志 |

从仓库 `bin/` 运行时，默认使用仓库的 `data/`；单独复制二进制到其他目录运行时，默认使用当前工作目录的 `data/`。日志在缓存目录同级的 `log/`。

删除 `data/indexes/` 会触发重新索引；删除 `data/projects.json` 会清除分组偏好。运行时不依赖预先存在的这些文件。

## 常见问题

- **网页打不开**：先运行 `./status.sh`，核对进程和实际端口，再检查 `log/server.log`。使用单二进制时检查启动进程和健康接口，不要只凭启动命令返回就宣布成功。
- **没有会话**：确认运行服务的系统用户、`CODEX_HOME` 或 `--codex-home`；目录中必须实际存在 JSONL 文件。目录内部的符号链接不跟随扫描。
- **端口被占用**：指定其他端口，或脚本方式设置 `PORT=0`，再使用实际返回地址。
- **提示格式错误 / 无法执行**：核对系统和 CPU 架构；macOS / Linux 下载后需要 `chmod +x`。如系统要求确认下载程序的运行权限，交由用户按系统提示处理。
- **首次统计不完整**：等待索引完成，查看进度；不要把局部统计当作整个会话结果。
- **部分内容无法还原**：工具只能展示文件实际保存的信息。已截断、加密或未保存的内容无法恢复；暂不解压 `.jsonl.zst`。

需要修改程序、更新构建产物或研究格式时，转到 [开发 agent 指南](AGENTS.md)；格式适配依据见 [格式与来源说明](docs/format.md)。
