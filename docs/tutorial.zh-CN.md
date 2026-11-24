# reup 使用教程

[English](tutorial.md) | 简体中文

reup 用来集中查找本机 AI 编程 CLI 的历史会话，并通过对应 agent 的原生命令恢复会话。目前支持 Antigravity CLI（`agy`）、CodeBuddy、Kimi Code、OpenCode，以及仅列表的 DeepSeek Harness（`dsh`）。

本文面向日常使用。开发与真机验证说明见 [real-world-testing.md](real-world-testing.md)。

## 1. 安装与准备

### 支持环境

- macOS 或 Linux，arm64 / amd64
- 至少一个受支持的 agent CLI
- 该 agent 已经在本机产生过会话

`agy` 当前仅支持 Linux，其他 provider 支持 macOS 和 Linux。

### 安装 reup

使用安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

或者通过 Go 安装：

```bash
go install github.com/QuietSugar/reup@latest
```

确认安装成功：

```bash
reup --version
```

升级时重新执行对应的安装命令即可。

### 检查 provider 状态

```bash
reup provider list
```

输出中的关键列：

- `PROVIDER`：reup 使用的来源名称。
- `STATE`：在 reup 配置中启用还是停用。
- `STORAGE`：CLI 和会话存储是否被检测到；`missing` 表示当前不可用。

如果某个 agent 显示 `missing`，先确认它的 CLI 在 `PATH` 中，并且已经创建过会话。不同 provider 的检测条件略有区别，不能只凭 agent 已安装判断。

## 2. 五分钟快速上手

### 第一步：打开选择器

```bash
reup
```

常用操作：

- `↑` / `↓` 或 `j` / `k`：移动选择。
- `/`：输入过滤条件，回车应用。
- `tab`：在全部来源和单个 provider 之间切换。
- `g`：切换工作区分组和平铺列表。
- `o` / `v`：切换详情和侧栏面板。
- `d`：隐藏会话；Kimi Code 使用官方归档流程。
- `P`：管理 provider 开关。
- `?`：查看按键和当前 provider 提示。
- `enter`：恢复选中的会话。
- `esc`：取消并退出。

终端宽度达到 100 列时会显示详情面板，达到 140 列时会显示工作区/agent 侧栏。

### 第二步：直接查看会话列表

不想进入交互界面时：

```bash
reup list
```

默认按工作区分组。使用平铺表格：

```bash
reup list --no-group
```

### 第三步：恢复会话

在选择器中选中会话并按 `enter`。reup 会：

1. 进入该会话记录的工作目录。
2. 调用对应 agent 的原生恢复命令。
3. agent 退出后返回 shell；如果目录发生切换，会打印一条 `cd <dir>` 提示。

父 shell 的当前目录不会被子进程改变，因此需要时可以参考这条提示自行切换。

## 3. 常见任务

### 只查看某个 provider

```bash
reup list --source agy
reup list --source codebuddy
reup list --source kimi-code
reup list --source opencode
reup list --source dsh
```

`--source` 也可用于交互选择器：

```bash
reup --source agy
```

即使某个 provider 已在配置中停用，显式指定 `--source` 仍可进行一次性查询。

### 按项目或时间筛选

```bash
# 项目名称包含 foo，忽略大小写
reup list --project foo

# 最近 7 天有活动的会话
reup list --days 7

# 指定日期
reup list --date 2026-11-23

# 不加时间限制
reup list --all
```

默认不加时间限制，`--all` 主要用于显式表达意图和兼容旧用法。

当前限制：`dsh` 暂不应用 `--project`、`--days` 和 `--date`；其他 provider 支持这些过滤器。

### 限制列表数量

```bash
reup list --limit 20
```

`--limit` 当前只在 `list` 模式生效，选择器暂不使用该限制。

### 查看详情或导出 JSON

```bash
# 默认显示前 5 个会话的详细卡片
reup list --full

# 显示前 10 个
reup list --full 10

# 输出 JSON
reup list --json
```

JSON 是平铺数组，包含 `source`、`session_id`、`cwd`、`workspace_id`、`workspace_root`、时间、标题、提示词和 `resume_argv` 等字段。

### 隐藏与恢复显示

在选择器中选中会话后按 `d`，再按提示确认。

查看隐藏列表：

```bash
reup hidden
```

恢复显示：

```bash
reup hidden restore SOURCE SESSION_ID
```

示例：

```bash
reup hidden restore agy 00000000-0000-0000-0000-000000000000
```

这里的隐藏列表保存在 reup 自己的配置中，不等于删除 agent 的会话文件。

### 停用或重新启用 provider

```bash
reup provider off opencode
reup provider on opencode
```

停用后，常规扫描和选择器不会再读取该 provider 的会话数据。

## 4. 各 provider 的行为差异

| Provider | 恢复方式 | 按 `d` 的行为 |
|---|---|---|
| Antigravity CLI（`agy`，仅 Linux） | `agy --conversation <id>` | 仅在 reup 中隐藏 |
| CodeBuddy | `codebuddy --resume <id>` | 仅在 reup 中隐藏 |
| Kimi Code | `kimi --session <id>` | 通过 Kimi 官方 API 归档，可恢复 |
| OpenCode | `opencode --session <id>` | 仅在 reup 中隐藏；真实删除需使用 OpenCode 自己的命令 |
| DeepSeek Harness（`dsh`） | 当前仅列表，不能从 reup 恢复 | 仅在 reup 中隐藏 |

在具体 provider 标签下按 `?`，可以查看该工具的官方会话管理入口：

- **agy**：运行 `agy`，输入 `/resume`，选中会话后按 **F4** 删除。
- **CodeBuddy**：`codebuddy project purge [path]` 清理一个项目的会话及其他本地状态，并非只清理单个会话；先加 `--dry-run` 预览。
- **Kimi Code**：运行 `kimi web`，在其全局 Web 页面中统一管理所有会话。
- **OpenCode**：在其 TUI 中按 **Ctrl+D** 交互删除会话。
- **DeepSeek Harness**：在 Harness 页面中归档或删除会话；reup 会识别其归档状态。

Kimi Code 的特殊规则：

- 没有运行 `kimi web` 时，确认归档后 reup 会通过 `kimi web --no-open` 启动本地服务；归档成功后通过官方 shutdown API 关闭它。
- 如果服务已经运行，reup 不会直接操作该实例，而会在弹窗中给出 Kimi Web UI 地址，供你自行处理。
- 自动启动服务只发生在确认归档的流程中，普通浏览和恢复不会启动它。

OpenCode 的真实删除可使用其官方命令：

```bash
opencode session delete <sessionID>
```

删除与 reup 的“隐藏”不同：删除会影响 OpenCode 的会话数据，隐藏只改变 reup 的显示。

## 5. 工作区与工作目录

reup 优先按会话的工作目录分组，因此同一个目录下由不同 agent 创建的会话会归在一起。没有目录信息时，才使用 provider 原生的 workspace/project ID。

### 工作目录已删除

如果项目被移动、改名或删除，列表会显示 `(directory missing)`，按 `enter` 时 reup 会拒绝启动 agent，并打印缺失路径。

确认需要恢复原目录后，可按提示重新创建：

```bash
mkdir -p "/path/from/the/error/message"
```

如果项目已经移动，应恢复会话数据所记录的原路径，或参考对应 agent 的官方方式迁移会话，不要直接修改其存储文件。

### CodeBuddy 路径漂移

对于 iCloud、Obsidian 等场景中记录路径已经变化、但会话文件位置仍有效的情况，reup 会尝试从 CodeBuddy 的项目目录名称重新推导正确 cwd。

## 6. 配置、隐私与自定义

### 配置文件

默认位置：

```text
~/.config/reup/config.json
```

其中保存 provider 开关和 reup 隐藏列表。可通过以下变量改变位置：

- `REUP_CONFIG`：覆盖完整配置文件路径。
- `XDG_CONFIG_HOME`：覆盖配置根目录。

首次运行时，如果存在旧的 `~/.config/resumer/config.json`，reup 会将其中的配置复制到新位置；显式设置 `REUP_CONFIG` 时不会执行这项迁移。

### 自定义帮助提示

创建：

```text
~/.config/reup/tips.md
```

或者设置：

```bash
export REUP_TIPS_FILE=/path/to/tips.md
```

自定义文件会替换选择器中的内置 provider 提示；文件缺失或为空时回退到内置提示。

### 存储位置覆盖

测试或非标准安装可使用：

| 环境变量 | 覆盖位置 |
|---|---|
| `REUP_AGY_HOME` | `~/.gemini/antigravity-cli` |
| `REUP_CODEBUDDY_HOME` | `~/.codebuddy` |
| `REUP_KIMI_HOME` | `~/.kimi-code` |
| `REUP_OPENCODE_DATA` | OpenCode 数据目录 |
| `REUP_DSH_HOME` | `~/.dsh` |

OpenCode 也遵循 `XDG_DATA_HOME`。

### 数据访问边界

- 普通扫描只读取 agent 已存在的会话元数据，不安装 hook，也不上传会话内容。
- reup 隐藏只修改 reup 自己的配置，不编辑 agent 的会话存储。
- Kimi 归档是唯一由 reup 主动调用的官方会话变更流程，需要你在弹窗中确认。
- 恢复会话时，实际后续行为由对应 agent CLI 负责。

## 7. 故障排查

### 提示 “no session providers available”

依次检查：

```bash
reup provider list
agy --help                 # 使用 Antigravity CLI 时
codebuddy --help           # 使用 CodeBuddy 时
kimi --help                # 使用 Kimi Code 时
opencode --help            # 使用 OpenCode 时
```

确认 CLI 在 `PATH` 中、会话存储存在，并至少产生过一个有效会话。若 provider 被停用：

```bash
reup provider on NAME
```

### 列表为空或找不到某个会话

先取消来源和时间限制进行定位：

```bash
reup list --all
reup list --source NAME --all
```

再检查：

- 是否设置了过窄的 `--project`、`--days` 或 `--date`。
- 会话是否被 `reup hidden` 隐藏。
- provider 是否被停用。
- Kimi 中只打开但未输入内容的空会话会被跳过。
- 已被对应 agent 归档或删除的会话不会重新出现。

### 恢复时提示找不到 agent 命令

确保对应 CLI 已安装并位于当前 shell 的 `PATH` 中。重新打开 shell 后再执行：

```bash
command -v agy
command -v codebuddy
command -v kimi
command -v opencode
```

### `dsh` 会话提示 “no resume command”

这是当前预期行为：DeepSeek Harness 在 reup 中仅支持浏览，不支持恢复。

### Kimi 提示服务已经运行

按弹窗给出的地址打开 Kimi Web UI，在 Kimi 自己的界面中归档或管理会话。reup 不会直接操作用户已经启动的服务实例。

### 需要进一步诊断

分别验证列表、JSON 和单 provider 查询：

```bash
reup list --all --no-group
reup list --all --json
reup list --source NAME --all
```

开发 fixture、mock agent 和真机验证方法见 [real-world-testing.md](real-world-testing.md)。
