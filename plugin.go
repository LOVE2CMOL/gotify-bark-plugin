package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gotify/plugin-api"
)

const (
	// queueSize is the number of notifications buffered while bark is slow.
	queueSize = 256
	// resolveRetryInterval is how often the plugin retries to determine the
	// Gotify base URL when it is neither configured nor known yet.
	resolveRetryInterval = 30 * time.Second
)

// Plugin is the gotify plugin instance, one per Gotify user.
type Plugin struct {
	userCtx  plugin.UserContext
	basePath string

	mu        sync.Mutex
	config    *Config
	location  *url.URL
	http      *http.Client
	appCache  *appCache
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	queue     chan gotifyMessage
	statusMsg string
}

func newPlugin(ctx plugin.UserContext) *Plugin {
	return &Plugin{
		userCtx:   ctx,
		appCache:  &appCache{},
		statusMsg: "尚未启用",
	}
}

// Enable implements plugin.Plugin.
func (p *Plugin) Enable() error {
	log.Printf("[bark] plugin enabled for user %q (id=%d)", p.userCtx.Name, p.userCtx.ID)
	p.start()
	return nil
}

// Disable implements plugin.Plugin.
func (p *Plugin) Disable() error {
	log.Printf("[bark] plugin disabled for user %q (id=%d)", p.userCtx.Name, p.userCtx.ID)
	p.stop()
	p.setStatus("已停用")
	return nil
}

// RegisterWebhook implements plugin.Webhooker. The webhook is a small health /
// test endpoint reachable at {gotify}/plugin/{id}/custom/bark.
func (p *Plugin) RegisterWebhook(basePath string, mux *gin.RouterGroup) {
	p.mu.Lock()
	p.basePath = basePath
	p.mu.Unlock()

	mux.GET("/bark", func(ctx *gin.Context) {
		cfg := p.snapshot()
		payload := gin.H{
			"plugin":  "gotify-bark-plugin",
			"version": Version,
			"running": p.isRunning(),
		}
		if cfg != nil {
			payload["enabled"] = cfg.Enabled
			payload["server_url"] = cfg.ServerURL
			payload["devices"] = len(splitKeys(cfg.DeviceKeys))
			payload["gotify_url"] = cfg.GotifyURL
		}
		ctx.JSON(http.StatusOK, payload)
	})
}

// --- lifecycle ---------------------------------------------------------------

// start launches the stream subscription and the forwarding worker. It never
// fails hard: problems are surfaced through the plugin display so that Gotify
// does not disable the plugin while the user is still filling in the config.
func (p *Plugin) start() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	cfg := p.config
	location := p.location
	p.mu.Unlock()

	if cfg == nil {
		p.setStatus("配置未就绪")
		return
	}
	if !cfg.Enabled {
		p.setStatus("插件已启用，但配置里的 enabled 仍为 false")
		return
	}
	if cfg.ClientToken == "" {
		p.setStatus("缺少 client_token：请在 Gotify 里创建一个 Client 并填入其令牌")
		return
	}
	if len(splitKeys(cfg.DeviceKeys)) == 0 {
		p.setStatus("缺少 device_keys：请把 Bark App 首页的推送 URL 里的设备密钥填入配置")
		return
	}
	baseURL := p.baseURL(cfg, location)
	if baseURL == "" {
		p.setStatus("无法确定 Gotify 地址：请打开一次本插件页面，或手动填写 gotify_url")
		p.scheduleResolveRetry()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := newHTTPClient(time.Duration(cfg.TimeoutSeconds) * time.Second)
	queue := make(chan gotifyMessage, queueSize)

	p.mu.Lock()
	p.cancel = cancel
	p.started = true
	p.http = client
	p.queue = queue
	p.mu.Unlock()

	sub := &streamSubscriber{
		baseURL: baseURL,
		token:   cfg.ClientToken,
		client:  client,
		debug:   cfg.Debug,
		onMessage: func(msg gotifyMessage) {
			select {
			case queue <- msg:
			default:
				log.Printf("[bark] queue full (%d), dropping gotify message id=%d", queueSize, msg.ID)
			}
		},
	}

	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		sub.run(ctx)
	}()
	go func() {
		defer p.wg.Done()
		p.forwardLoop(ctx, queue)
	}()

	p.setStatus(fmt.Sprintf("运行中：订阅 %s，转发到 %s（%d 个设备）", baseURL, cfg.ServerURL, len(splitKeys(cfg.DeviceKeys))))
	log.Printf("[bark] forwarding started: gotify=%s bark=%s devices=%d", baseURL, cfg.ServerURL, len(splitKeys(cfg.DeviceKeys)))
}

// stop tears the running goroutines down and waits for them to finish.
func (p *Plugin) stop() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	wasStarted := p.started
	p.started = false
	p.queue = nil
	p.mu.Unlock()

	if !wasStarted {
		return
	}
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
	log.Printf("[bark] forwarding stopped for user %q", p.userCtx.Name)
}

// restart applies a configuration change to a running subscription.
func (p *Plugin) restart() {
	p.mu.Lock()
	running := p.started
	p.mu.Unlock()
	if !running {
		return
	}
	p.stop()
	p.start()
}

// scheduleResolveRetry retries start() until the Gotify base URL is known.
func (p *Plugin) scheduleResolveRetry() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			time.Sleep(resolveRetryInterval)
			p.mu.Lock()
			started := p.started
			cfg := p.config
			location := p.location
			p.mu.Unlock()
			if started || cfg == nil || !cfg.Enabled {
				return
			}
			if p.baseURL(cfg, location) == "" {
				continue
			}
			p.start()
			return
		}
	}()
}

func (p *Plugin) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started
}

func (p *Plugin) setStatus(msg string) {
	p.mu.Lock()
	p.statusMsg = msg
	p.mu.Unlock()
}

func (p *Plugin) snapshot() *Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

// baseURL returns the Gotify base URL, preferring the explicit configuration
// and falling back to the address of the request that rendered the plugin page.
func (p *Plugin) baseURL(cfg *Config, location *url.URL) string {
	if cfg != nil && cfg.GotifyURL != "" {
		return cfg.GotifyURL
	}
	if location != nil && location.Host != "" {
		u := *location
		u.Path, u.RawQuery, u.Fragment = "", "", ""
		return strings.TrimRight(u.String(), "/")
	}
	return ""
}

// rememberLocation stores the server location seen by GetDisplay and starts the
// plugin if it was only waiting for that piece of information.
func (p *Plugin) rememberLocation(location *url.URL) {
	if location == nil || location.Host == "" {
		return
	}
	p.mu.Lock()
	p.location = location
	cfg := p.config
	started := p.started
	p.mu.Unlock()
	if cfg == nil || started || !cfg.Enabled || cfg.GotifyURL != "" {
		return
	}
	p.start()
}

// --- forwarding --------------------------------------------------------------

// forwardLoop consumes queued Gotify messages and pushes them to Bark one by
// one, so notifications keep their original order.
func (p *Plugin) forwardLoop(ctx context.Context, queue <-chan gotifyMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-queue:
			if !ok {
				return
			}
			p.forward(ctx, msg)
		}
	}
}

func (p *Plugin) forward(ctx context.Context, msg gotifyMessage) {
	p.mu.Lock()
	cfg := p.config
	client := p.http
	cache := p.appCache
	p.mu.Unlock()

	if cfg == nil || client == nil {
		return
	}
	if !p.shouldForward(cfg, msg) {
		return
	}

	// Application metadata is only fetched when something actually needs it:
	// the Bark group, an {appname} placeholder, or the "auto" icon.
	app := applicationInfo{}
	gotifyBase := ""
	if cfg.GroupByApp || cfg.Icon == iconAuto ||
		strings.Contains(cfg.URLTemplate, "{appname}") ||
		strings.Contains(cfg.CopyTemplate, "{appname}") {
		if base, err := httpBaseURL(p.baseURL(cfg, p.currentLocation())); err == nil {
			gotifyBase = base
			app = cache.get(ctx, client, base, cfg.ClientToken, msg.ApplicationID)
		}
	}

	for _, key := range splitKeys(cfg.DeviceKeys) {
		push := buildBarkPush(cfg, msg, key, app, gotifyBase)
		if cfg.DryRun {
			if cfg.EncryptKey != "" {
				log.Printf("[bark] dry-run: would push to device %s (encrypted, mode=%s): %+v", key, cfg.EncryptMode, push)
			} else {
				log.Printf("[bark] dry-run: would push to device %s: %+v", key, push)
			}
			continue
		}
		if err := sendBarkPush(ctx, client, cfg, key, push); err != nil {
			log.Printf("[bark] push to device %s failed (gotify id=%d): %v", key, msg.ID, err)
			continue
		}
		if cfg.Debug {
			log.Printf("[bark] pushed gotify id=%d to device %s", msg.ID, key)
		}
	}
}

func (p *Plugin) currentLocation() *url.URL {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.location
}

// shouldForward applies the message filters.
func (p *Plugin) shouldForward(cfg *Config, msg gotifyMessage) bool {
	if msg.priority() < cfg.MinPriority {
		if cfg.Debug {
			log.Printf("[bark] skip gotify id=%d: priority %d < min_priority %d", msg.ID, msg.priority(), cfg.MinPriority)
		}
		return false
	}
	if cfg.SkipTitlePrefix != "" && strings.HasPrefix(msg.Title, cfg.SkipTitlePrefix) {
		if cfg.Debug {
			log.Printf("[bark] skip gotify id=%d: title %q starts with %q", msg.ID, msg.Title, cfg.SkipTitlePrefix)
		}
		return false
	}
	return true
}

// buildBarkPush maps one Gotify message onto a Bark push request.
func buildBarkPush(cfg *Config, msg gotifyMessage, deviceKey string, app applicationInfo, gotifyBase string) barkPush {
	body := msg.Message
	if cfg.IncludeExtras && len(msg.Extras) > 0 {
		if extras, err := json.Marshal(msg.Extras); err == nil {
			body = strings.TrimRight(body, "\n") + "\n\n" + string(extras)
		}
	}

	level := cfg.Level
	if level == levelAuto {
		level = barkLevelForPriority(msg.priority())
	}

	icon := cfg.Icon
	if icon == iconAuto {
		icon = resolveAppIcon(gotifyBase, app.Image)
	}

	push := barkPush{
		Body:      body,
		DeviceKey: deviceKey,
		Title:     msg.Title,
		Level:     level,
		Sound:     cfg.Sound,
		Icon:      icon,
		AutoCopy:  cfg.AutoCopy,
		Action:    cfg.Action,
		IsArchive: cfg.Archive,
		TTL:       cfg.TTL,
	}

	// Bark's `call` keeps the phone ringing for 30 seconds. Only genuinely
	// urgent messages may do that, otherwise a single chatty application could
	// make the phone unusable — so it requires Gotify priority >= 9 even when
	// call is enabled in the configuration.
	if cfg.Call != "" && msg.priority() >= callMinPriority {
		push.Call = cfg.Call
	}

	if cfg.Badge >= 0 {
		badge := cfg.Badge
		push.Badge = &badge
	}
	if push.TTL > 0 && push.IsArchive == "" {
		push.IsArchive = "1"
	}

	switch {
	case cfg.GroupByApp && app.Name != "":
		push.Group = app.Name
	case cfg.DefaultGroup != "":
		push.Group = cfg.DefaultGroup
	}

	if cfg.URLTemplate != "" {
		push.URL = expandTemplate(cfg.URLTemplate, msg, app.Name, true)
	}
	switch {
	case cfg.CopyTemplate != "":
		push.Copy = expandTemplate(cfg.CopyTemplate, msg, app.Name, false)
	case push.AutoCopy == "1":
		push.Copy = body
	}
	return push
}

// expandTemplate replaces the placeholders supported in url_template and
// copy_template. Values are query-escaped when the result is used as a URL.
func expandTemplate(tpl string, msg gotifyMessage, appName string, escape bool) string {
	values := map[string]string{
		"{appid}":     fmt.Sprintf("%d", msg.ApplicationID),
		"{appname}":   appName,
		"{title}":     msg.Title,
		"{message}":   msg.Message,
		"{priority}":  fmt.Sprintf("%d", msg.priority()),
		"{messageid}": fmt.Sprintf("%d", msg.ID),
	}
	if escape {
		// PathEscape, not QueryEscape: a placeholder usually lands in the path
		// (".../{appname}/{messageid}"), where QueryEscape's "+" would stay a
		// literal plus instead of a space. PathEscape is also valid inside a
		// query string, so one encoding covers both positions.
		for k, v := range values {
			values[k] = url.PathEscape(v)
		}
	}
	// Deterministic order keeps the replacement independent of map iteration.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := tpl
	for _, k := range keys {
		out = strings.ReplaceAll(out, k, values[k])
	}
	return out
}

// splitKeys splits the normalised device key list.
func splitKeys(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// GetDisplay is implemented in display.go.
func (p *Plugin) display(location *url.URL) string {
	p.rememberLocation(location)
	return p.renderDisplay(location)
}
