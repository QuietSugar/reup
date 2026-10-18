[English](README.md) | [简体中文](README.zh-CN.md)

# reup

[![CI](https://github.com/QuietSugar/reup/actions/workflows/ci.yml/badge.svg)](https://github.com/QuietSugar/reup/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](#安装)

> 浏览、恢复、管理 CodeBuddy / Kimi Code / OpenCode 的会话（DeepSeek Harness 仅列表）——一个选择器，零依赖。

单个静态二进制。不需要 Python、fzf，无需任何配置。打开选择器，跨 provider 浏览全部最近的 AI CLI 会话并预览详情，回车即回到对话。

## 安装

```bash
curl -fsSL https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

访问不了 GitHub 时，用加速前缀拉这个脚本，脚本下载资产时会自动沿用同一个
前缀（也可以显式设置 `GH_PROXY`）：

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

或使用 Go：

```bash
go install github.com/QuietSugar/reup@latest
```

支持 macOS 与 Linux（arm64/amd64）。Windows 在路线图中。

## 使用

```bash
reup              # 交互式选择器
reup list         # 按工作区分组列出全部会话
reup --help       # 其余全部用法
```

选择器按键：`↑↓` 浏览 · `/` 过滤 · `tab` 切换 provider 页签 · `w` 侧栏 ·
`o`/`v` 开关面板 · `g` 分组模式 · `d` 隐藏/归档 · `P` provider 管理 · `?` 帮助 ·
`enter` 恢复 · `esc` 取消。鼠标同样可用：点击顶栏页签切换 agent，点击侧栏
workspace 过滤，点击会话行选中（双击恢复），滚轮滚动列表。注意：开启鼠标
捕获后，选中文字需要按住 Shift 拖动。屏幕最后一行始终显示当前上下文可用的
按键。会话列表带单行列头（`age` `agent` `title`）；终端宽度 ≥100 列时右侧
打开详情面板，≥140 列时左侧出现 workspace/agent 侧栏——`o`/`v` 可分别开关。
行首的标记列（`sz`）是粗略的会话体量条，颜色灰 → 黄 → 红：空白 <20 条消息，
`▁` 20–49，`▄` 50–149，`█` 150+。

**会话管理**：选中会话按 `d` 即隐藏。

- **kimi-code**：通过 kimi 自己的 API 官方归档——可恢复，且在 kimi 自己的
  界面中同样隐藏。若 `kimi web` 未运行，reup 会以脱离终端的方式启动它
  （不占终端、不弹浏览器），完成归档后再通过其优雅的 `/api/v1/shutdown`
  关闭；你自己启动的服务器不会被触碰，弹窗会指向它的 Web UI。
- **opencode / codebuddy**：仅在 reup 中隐藏（两者都没有针对存储历史的
  官方单会话隐藏/删除机制——经源码验证，codebuddy 的
  `DELETE /api/v1/sessions/:id` 在实例外是静默空操作）。文件不动，agent
  照常显示该会话，token 统计不受影响。

`reup hidden` 列出已隐藏会话；`reup hidden restore SOURCE SESSION_ID`
恢复显示。

**从 resumer 迁移**：首次运行时，`~/.config/resumer/config.json`（含隐藏
列表和 provider 开关）与 `tips.md` 会自动拷贝到 `~/.config/reup/`；旧目录
原样保留。

常用参数（选择器与 `list` 通用）：`--days N`（只看最近 N 天——**默认不限
时间，列出全部会话**）、`--date YYYY-MM-DD`、`--all`（保留兼容，现为默认）、
`--project foo`、`--source codebuddy|kimi-code|opencode|dsh`、`--limit N`。
`list` 模式另有 `--no-group`（平铺表格代替分组）、`--json`、`--full [N]`。
JSON 的 `asst_count` 字段与详情面板中的 assistant 活动数是各 provider 粗略
的体量估计：统计口径不同，数值不精确，也不能跨 provider 直接比较。

帮助弹窗（`?`）默认内置各 provider 的提示。要替换为自己的纯文本提示，
创建 `~/.config/reup/tips.md` 或设置 `REUP_TIPS_FILE` 指向其他文件。文件在
选择器启动时读取；缺失或为空时回退到内置提示。

## 工作区（Workspace）

`reup list` 与选择器按 **工作区** 分组：在同一目录打开过的会话跨 provider
归为一组。分组头形如：

```
── /home/xu/git-repo/my-project  ·  3 sessions  ·  codebuddy, kimi-code
```

分组键就是会话的工作目录，多个 agent 用过的目录自然归为一组。当 provider
记录了原生 workspace/project 标识（Kimi Code 的 `wd_<slug>_<hash>` 会话桶与
`workspaces.json`；OpenCode 的 `workspace_id`/`project_id`），则以其作为
分组身份——也是没有目录信息时的最后回退键。既无目录也无原生 id 的会话
按 provider 归入 `(no workspace)`。

`--json` 保持平铺数组（如需下游自行分组，可直接使用 `workspace_id`/
`workspace_root` 字段）。`reup list --no-group` 输出旧的平铺表格；
`--source NAME` 可限定单一 provider（此时不做跨 provider 合并）。

## 启用 / 停用 provider

每个 provider 都可以关闭；被停用的 provider **完全不被扫描和解析**——不
遍历存储、不读文件/数据库——直到重新启用。

```bash
reup provider             # 状态表——在 picker 内按 P 交互切换
reup provider list        # 同一张表，显式输出
reup provider off opencode
reup provider on opencode
```

状态持久化在 `~/.config/reup/config.json`（`$REUP_CONFIG` 覆盖文件路径，
`$XDG_CONFIG_HOME` 覆盖其目录）。被停用的 provider 不会出现在常规扫描与
选择器的 `tab` 页签循环中。显式指定 `reup list --source opencode` 时，被
停用的 provider 依然可用——该参数是一次性的明确请求。

## Provider 支持

| Provider | 会话来源 | 恢复命令 |
|---|---|---|
| CodeBuddy CLI | `~/.codebuddy/projects/**/*.jsonl` | `codebuddy --resume <id>` |
| Kimi Code CLI | `~/.kimi-code/sessions/<workDirKey>/<id>/` | `kimi --session <id>` |
| OpenCode | `~/.local/share/opencode/opencode.db`（v1.1+；推荐 v2） | `opencode --session <id>` |
| DeepSeek Harness（`dsh`） | `~/.dsh/sessions/<workDirKey>/<id>/session.v4.jsonl[.zstd]` | 仅列表——harness 无恢复入口，回车会提示 "no resume command" |

仓库内提交的 opencode 测试数据库用以下命令重新生成：
`go run ./tools/opencodefixture tests/fixtures/opencode-home/opencode.db`。

多 agent 格式研究参考 [TokenTracker](https://github.com/xiufengsun/TokenTracker)：
其检查快照的 README 列出 42 个受支持的 AI 工具（含 CodeBuddy），其 CodeBuddy
笔记描述了 `~/.codebuddy/projects/**/*.jsonl` 会话存储及其源自 Claude Code
的目录布局（检查快照：`daa6c55099d34458ff773f6c1edd777f16fb7693`）。reup
只读取浏览与恢复所需的会话元数据。

CodeBuddy 的存储格式与 TokenTracker 及其 CLI 恢复参考做了交叉核对。Kimi
Code 与 OpenCode 的行为分别对照 kimi-code **2.1.1** 与 opencode **1.18.33**
源码验证；更早版本仅尽力兼容。真机验证步骤见
[docs/real-world-testing.md](docs/real-world-testing.md)。

Kimi 会留下"打开过但未使用"的会话目录（`state.json` 无标题无 prompt，
`wire.jsonl` 只有生命周期事件）。reup 会跳过这些，与 Kimi 自己的会话列表
一致——你只是打开过 Kimi 的目录不会显示为会话。

reup 还修复了一个现实中的痛点：当会话记录的 cwd 已失效（iCloud/Obsidian
路径漂移）时，它会从会话文件位置重新推导正确的项目目录，让
`codebuddy --resume` 真正可用。

当会话的工作目录已被删除——项目改名、移动、删除，或会话数据被带到了
另一台机器——该会话无法恢复。reup 在 `reup list` 与选择器中把项目列显示
为 `(deleted)`，回车时拒绝启动 agent CLI，并打印缺失路径与解锁命令，而不
是 CLI 自己那句含混的 `created under a different directory`：

```
error: cannot resume [kimi-code] <session id> — its working directory has been deleted:
       /home/xu/git-repo/my-project
       the agent CLI cannot start there, so reup did not run the resume command.
       to resume this session, recreate the directory first:
         mkdir -p "/home/xu/git-repo/my-project"
       then run reup again and select this session.
```

<details>
<summary>开发</summary>

```bash
git clone https://github.com/QuietSugar/reup.git
cd reup
go build -o reup .
./tests/run-qa.sh   # go vet + 全量测试（无外部依赖）
```

测试套件包含 PTY 驱动的集成测试，用 `tests/fixtures/` 中的夹具端到端驱动
真实 TUI（选择器 → 过滤 → 选中 → 执行）。

发布全自动：推送 `v*` 标签即由 goreleaser 构建二进制。

路线图：Windows 支持。

</details>

---

MIT © 2026 Jintae Song · QuietSugar — 详见 [LICENSE](LICENSE)。
