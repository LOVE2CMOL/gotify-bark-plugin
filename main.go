// Command gotify-bark-plugin is a Gotify plugin that mirrors every notification
// arriving at a Gotify server to one or more Bark (iOS) devices.
//
// It must be built as a Go plugin:
//
//	go build -buildmode=plugin -o bark.so .
package main

import (
	"net/url"

	"github.com/gotify/plugin-api"
)

// Version is the plugin version reported to Gotify.
const Version = "1.2.1"

// ModulePath identifies this plugin. Gotify refuses plugins with an empty path.
const ModulePath = "github.com/dsh/gotify-bark-plugin"

// GetGotifyPluginInfo returns gotify plugin info.
func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		Version:     Version,
		Author:      "DSH",
		Name:        "Bark Forwarder",
		Website:     "https://github.com/Finb/Bark",
		Description: "把 Gotify 收到的每条通知实时镜像推送到 Bark（iOS），支持自建 bark-server、端到端加密、优先级过滤、铃声/分组/图标等完整 Bark 参数。",
		License:     "MIT",
		ModulePath:  ModulePath,
	}
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return newPlugin(ctx)
}

// GetDisplay implements plugin.Displayer.
func (p *Plugin) GetDisplay(location *url.URL) string {
	return p.display(location)
}

// Ensure the compiler checks that the instance implements every interface we rely on.
var (
	_ plugin.Plugin     = (*Plugin)(nil)
	_ plugin.Displayer  = (*Plugin)(nil)
	_ plugin.Configurer = (*Plugin)(nil)
	_ plugin.Webhooker  = (*Plugin)(nil)
)

func main() {
	panic("this plugin should be built with: go build -buildmode=plugin")
}
