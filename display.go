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
	if cfg != nil {
		if cfg.ServerURL != "" {
			server = cfg.ServerURL
		}
		gotify = cfg.GotifyURL
		if cfg.Level != "" {
			level = cfg.Level
		}
	}
	return fmt.Sprintf(`enabled: true
# Gotify 地址；留空则自动使用当前访问地址，Docker 部署建议显式填写
gotify_url: %q
# Gotify 客户端令牌（WebUI -> Clients -> Create Client）
client_token: "gtfy..."
# Bark 设备密钥，多个用逗号分隔
device_keys: "yourDeviceKey"
# Bark 服务端（自建示例：http://192.168.1.10:8080）
server_url: %q
group_by_app: true
default_group: ""
level: %q
sound: ""
icon: ""
call: ""
auto_copy: ""
copy_template: ""
url_template: ""
action: ""
archive: ""
ttl: 0
badge: -1
min_priority: -2
skip_title_prefix: ""
include_extras: false
timeout_seconds: 10
debug: false
dry_run: false
`, gotify, server, level)
}
