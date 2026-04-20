# nanobot-go

Go 语言实现的 [nanobot](https://github.com/HKUDS/nanobot) 轻量级 AI Agent 框架（MVP 版本）。

本仓库是 Python 版 nanobot 的 **核心** 移植，聚焦 Agent 循环与必要周边：

- **Agent Turn Loop**：`provider.chat` → tool calls → `provider.chat` → final，流式 delta + think-strip + 中途 injection
- **Provider 抽象**：OpenAI 兼容 + Anthropic（含 extended thinking）
- **内置工具**：`read_file`/`write_file`/`edit_file`/`list_dir`/`exec`/`glob`/`grep`/`web_search`/`web_fetch`/`message`/`my`/`spawn`/`cron`
- **MCP 客户端**：stdio 传输，自动把远端工具暴露为 `mcp_<server>_<tool>`
- **会话与记忆**：JSONL 会话存储 + Memory 两层（MEMORY.md 长期记忆 + history.jsonl 追加日志）+ Dream 简化双阶段
- **上下文装配**：skills 发现 + Go 模板渲染 + runtime block 注入
- **通道**：
  - CLI 交互模式（`nanobot agent`）
  - OpenAI 兼容 HTTP API（`nanobot serve`，SSE 流式）
  - WebSocket（`nanobot gateway`，含 `ready`/`delta`/`stream_end` 事件）
- **自动化**：cron 定时任务（at/every/cron 三种语法，持久化到 jobs.json） + heartbeat 周期任务 + 空闲会话 TTL 压缩（AutoCompact）
- **安全**：工作区路径沙箱 + SSRF 出站拦截 + deny-pattern 命令过滤 + Linux 下 `bwrap` 可选包装

**不在 MVP 范围**：Telegram/Feishu/WeChat/Slack/Discord/QQ/Matrix/WhatsApp/Email/DingTalk/WeCom/Teams 等 IM 通道；WebUI 构建物；OAuth 登录（GitHub Copilot / OpenAI Codex）。

## 目录结构

```
cmd/nanobot/           CLI 子命令：agent / serve / gateway / status / onboard
internal/
  bus/                 InboundMessage / OutboundMessage 通道
  config/              ~/.nanobot/config.json 解析（含 ${VAR} 与 NANOBOT_* 覆盖）
  session/             per-key JSONL 会话存储
  memory/              MEMORY.md + history.jsonl + 游标
  skills/              workspace + builtin 双源 YAML frontmatter 发现
  templates/           embed 的 Go text/template 模板
  ctxbuilder/          system prompt + runtime block 装配
  provider/            Provider 接口、重试策略、spec registry
    openai/            OpenAI Chat Completions（含 SSE）
    anthropic/         Anthropic Messages API（含 thinking_blocks）
  tools/               Tool 接口 + Registry + JSON-schema 校验
    fs/ shell/ web/ search/ message/ self/ spawn/ crontool/ mcpwrap/
  mcp/                 MCP stdio 客户端
  runner/              AgentRunner turn loop
  loop/                AgentLoop bus I/O + 会话锁 + 流式 hook
  hook/                AgentHook 接口 + CompositeHook
  autocompact/         TTL 空闲压缩
  consolidator/        token-budget 归档
  subagent/            fire-and-forget 子 agent 任务
  command/             slash-command 路由器 + builtins
  cron/                持久化定时器（at / every / cron）
  heartbeat/           HEARTBEAT.md 周期触发
  gitstore/            工作区 memory 文件的 git 版本化（shell-out 到 git）
  dream/               双阶段记忆整理
  security/            SSRF + 路径沙箱
channels/
  base/                Channel 接口 + ChannelManager + CLIChannel
  api/                 OpenAI 兼容 HTTP + SSE
  websocket/           零依赖 WebSocket 服务
skills/                Go 原生内置 skills
  memory/ cron/ github/ weather/ summarize/ tmux/ skillcreator/ my/
docs/
  analysis.md          Python 版架构分析 + Go 移植契约
```

## 快速开始

```bash
# 1) 安装 Go 1.23+
# 2) 构建
make build

# 3) 初始化配置
./bin/nanobot onboard --config ~/.nanobot/config.json

# 4) 在 config.json 里填入 providers.openai.apiKey 或 providers.anthropic.apiKey
# 或者通过环境变量：
export NANOBOT_PROVIDERS__OPENAI__APIKEY=sk-...

# 5) 交互模式
./bin/nanobot agent

# 或：单次提问后退出
./bin/nanobot agent -m "给我讲一个 Go 中 context 的典型用法"

# 或：跑 OpenAI 兼容 API 服务
./bin/nanobot serve --port 8900
# 另一个终端
curl http://127.0.0.1:8900/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'

# 或：gateway 同时启动 API + WebSocket
./bin/nanobot gateway
```

## 配置要点

参考 [config.sample.json](./config.sample.json)。关键节：

| 节 | 作用 |
| --- | --- |
| `agents.defaults` | 模型、温度、token、工作区、TTL、时区 |
| `providers.<name>` | 每个 Provider 的 apiKey/apiBase/extraHeaders |
| `api` | `/v1/*` 服务 host/port/timeout |
| `gateway` | 长连接 gateway + heartbeat |
| `tools.exec` | shell 工具的沙箱、超时、deny/allow |
| `tools.web` | web_search provider（tavily/brave/duckduckgo）+ key |
| `tools.mcpServers` | MCP 服务器映射 `name → {type,command,args,env,url}` |

配置内字符串支持 `${ENV_VAR}` 展开；同时环境变量 `NANOBOT_AGENTS__DEFAULTS__MODEL`、`NANOBOT_PROVIDERS__OPENAI__APIKEY` 等会覆盖同名字段（嵌套用 `__`、字段为 camelCase 大写）。

## 架构与移植说明

见 [docs/analysis.md](docs/analysis.md)：包含 Mermaid 部署图、单次用户输入时序图、核心算法伪代码（Turn Loop / AutoCompact / Consolidator / 重试策略）、每个 Python 模块在 Go 端的落位映射。

## 开发

```bash
make test       # go test ./...
make vet        # go vet
make build      # bin/nanobot
make test-race  # race detector
```

所有包均有独立单元测试或端到端测试（`channels/api` 跑通 JSON / SSE 通路；`cmd/nanobot` 做组合根烟雾测试；`internal/loop` 跑 fake provider + 工具调用 + 会话持久化）。

## License

MIT — 对齐上游 nanobot。
