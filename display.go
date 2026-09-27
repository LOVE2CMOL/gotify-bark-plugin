package main

import (
	"fmt"
	"net/url"
	"strings"
)

// renderDisplay produces the markdown shown on the plugin page. It doubles as
// the setup guide and as the place where connection problems are reported.
func (p *Plugin) renderDisplay(location *url.URL) string {
	p.mu.Lock()
	cfg := p.config
	status := p.statusMsg
	basePath := p.basePath
	started := p.started
	p.mu.Unlock()

	var b strings.Builder
	b.WriteString("### Bark Forwarder\n\n")
	b.WriteString("把 Gotify 收到的**每一条**通知实时镜像推送到 Bark（iOS）。\n\n")

	// --- status ----------------------------------------------------------
	b.WriteString("**状态：** ")
	if cfg == nil {
		b.WriteString("配置未加载\n\n")
	} else if !cfg.Enabled {
		b.WriteString("未开启（把配置里的 `enabled` 改成 `true`）\n\n")
	} else if started {
		b.WriteString("✅ 运行中\n\n")
	} else {
		b.WriteString("⚠️ 未运行\n\n")
	}
	if status != "" {
		fmt.Fprintf(&b, "> %s\n\n", status)
	}

	// --- detected location ----------------------------------------------
	if location != nil && location.Host != "" {
		fmt.Fprintf(&b, "**当前访问地址：** `%s://%s`\n\n", location.Scheme, location.Host)
		if cfg != nil && cfg.GotifyURL == "" {
			b.WriteString("> 若该地址在 Gotify 容器内部不可访问（Docker 里很常见），请手动填写 `gotify_url`，例如 `http://gotify:80`。\n\n")
		}
	}

	// --- setup steps -----------------------------------------------------
	b.WriteString("#### 配置步骤\n\n")
	b.WriteString("1. **创建客户端令牌**：Gotify 网页 → `Clients` → `Create Client`，名字随便填（如 `bark`）。\n")
	b.WriteString("   创建后弹出的令牌（形如 `gtfy...` 或一串随机字符）**只显示一次**，复制到下面的 `client_token`。\n")
	b.WriteString("2. **复制 Bark 设备密钥**：打开 Bark App 首页，复制测试 URL 中间那段 key（形如 `ynJ5Ft4atkMkWeo2PAvFhF`），填入 `device_keys`。\n")
	b.WriteString("   多个设备用逗号分隔，例如 `key1,key2`。\n")
	b.WriteString("3. **填写 Bark 服务端**：自建服务填 `server_url: http://你的IP:8080`；用官方服务则填 `https://api.day.app`。\n")
	b.WriteString("4. 保存配置后确认 `enabled: true`，本页状态应变为「✅ 运行中」。\n\n")
	b.WriteString("> **只有 `device_keys`、`server_url`、`client_token` 三项是必填的**（`gotify_url` 一般可自动识别）；下面示例里的其余配置项保持默认即可，每一项都附有中文注释，需要时再改。\n\n")

	b.WriteString("#### 配置示例\n\n```yaml\n")
	b.WriteString(exampleConfig(cfg))
	b.WriteString("```\n\n")

	// --- health endpoint -------------------------------------------------
	if basePath != "" {
		loc := &url.URL{Path: basePath}
		if location != nil && location.Host != "" {
			loc.Scheme = location.Scheme
			loc.Host = location.Host
		}
		loc = loc.ResolveReference(&url.URL{Path: "bark"})
		fmt.Fprintf(&b, "#### 自检接口\n\n`GET %s` 返回当前运行状态，可用于探活。\n\n", loc.String())
	}

	// --- notes -----------------------------------------------------------
	b.WriteString("#### 说明\n\n")
	b.WriteString("- 插件通过 Gotify 的 `/stream` WebSocket 订阅本用户的全部通知（包括其它应用推送的消息），再调用 Bark 的 `POST /push` 转发，因此**不需要修改 Gotify 本体**。\n")
	b.WriteString("- 填写 `encrypt_key` 后改用 Bark 的端到端加密：请求变为 `POST /{device_key}`，正文经 AES-256 加密，**服务端无法读取内容**，只有你的 iPhone 能解密。密钥必须与 Bark App 里「加密」设置的一致（32 位）。\n")
	b.WriteString("- 消息按到达顺序逐条转发；Bark 暂时不可用时会自动重试 3 次，失败只记录日志、不影响 Gotify 本身。\n")
	b.WriteString("- `min_priority` 可按优先级过滤；`skip_title_prefix` 可过滤标题前缀（例如用它跳过自己发出的回环消息）。\n")
	b.WriteString("- 想验证是否生效，可在 Gotify 里对任意应用点「发送测试消息」，或直接 `curl` 一次 push API。\n")

	return b.String()
}

// exampleConfig renders a ready-to-paste YAML config, seeded with whatever the
// user already configured.
func exampleConfig(cfg *Config) string {
	server := DefaultServerURL
	gotify := ""
	level := "active"
	encMode := encryptModeCBC
	if cfg != nil {
		if cfg.ServerURL != "" {
			server = cfg.ServerURL
		}
		gotify = cfg.GotifyURL
		if cfg.Level != "" {
			level = cfg.Level
		}
		if cfg.EncryptMode != "" {
			encMode = cfg.EncryptMode
		}
	}
	return fmt.Sprintf(`enabled: true

# ══ 必须填写（四选三，加密可选）══════════════════════
# Bark 设备密钥：App 首页推送 URL 中间那段；多台设备用逗号分隔
# 注意每台设备必须用各自的 key（同一个 key 两台手机只有一台能收到）
device_keys: "yourDeviceKey"
# Bark 服务端：自建填 http://192.168.1.10:8080，官方服务填 https://api.day.app
server_url: %q
# Gotify 地址：留空则用你打开本页的地址；Docker 部署建议显式填写
gotify_url: %q
# Gotify 客户端令牌（WebUI -> Clients -> Create Client，只显示一次）
client_token: "gtfy..."

# ══ 端到端加密（可选，Bark App 里开了「推送加密」才填）═══
# App 端请选：算法 AES256 / 模式 CBC / Padding pkcs7（GCM 暂不支持）
# 32 位密钥，必须与 App 里填的完全一致；留空 = 明文推送
encrypt_key: ""
# cbc（默认，每条消息随机 IV）或 ecb，需与 App 里选的模式一致
encrypt_mode: %q
# CBC 固定 IV（16 位），一般留空，由插件每次随机生成
encrypt_iv: ""

# ══ 通知外观（可选，保持默认即可）═════════════════════
# true = 用 Gotify 应用名作为 Bark 里的分组（推荐）
group_by_app: true
# 上面为 false 或应用名为空时，统一使用的分组名
default_group: ""
# 中断级别：auto = 跟随 Gotify 优先级自动判断（推荐）
#   priority <1 -> passive，1-3 -> active，4-7 -> timeSensitive，>7 -> critical
# 也可固定为 active（默认）/ timeSensitive / passive / critical / 留空
level: %q
# 铃声名，例如 alarm、minuet；留空用 Bark 默认
sound: ""
# 通知图标 URL（iOS 15+）；填 auto = 直接用 Gotify 里给该应用设置的图标
icon: ""
# "1" = 允许响铃 30 秒；注意只有 Gotify 优先级 ≥ 9 时才会真正触发
call: ""
# 角标数字，-1 = 不改动
badge: -1

# ══ 点击通知时做什么（可选）═══════════════════════════
# 点击后跳转的 URL，占位符：{appid} {appname} {title} {message} {priority} {messageid}
url_template: ""
# "none" = 点击不跳转（只关掉通知）
action: ""
# "1" = 自动把正文复制到剪贴板
auto_copy: ""
# 复制的内容模板，留空则复制正文
copy_template: ""
# "1" = 在 Bark 的历史记录里保留这条
archive: ""
# 历史保留秒数，0 = Bark 默认；>0 会自动打开 archive
ttl: 0

# ══ 过滤 / 调试（可选）════════════════════════════════
# 低于此优先级的消息不推送，范围 -2 ~ 10
min_priority: -2
# 标题以此字符串开头则丢弃（可用来防回环）
skip_title_prefix: ""
# true = 把 Gotify 的 extras（JSON）追加到正文
include_extras: false
# 单次 Bark 请求超时（秒）
timeout_seconds: 10
# true = 打印每条转发/跳过的日志
debug: false
# true = 只记日志、不真正推送（调试试用）
dry_run: false
`, server, gotify, encMode, level)
}
