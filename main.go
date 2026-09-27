// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 DSH
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for more
// details.
//
// You should have received a copy of the GNU General Public License along with
// this program.  If not, see <https://www.gnu.org/licenses/>.

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
const Version = "1.2.3"

// ModulePath identifies this plugin. Gotify refuses plugins with an empty path.
const ModulePath = "https://github.com/love2cmol/gotify-bark-plugin"

// GetGotifyPluginInfo returns gotify plugin info.
func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		Version:     Version,
		Author:      "DSH",
		Name:        "Bark Forwarder",
		Website:     "https://github.com/Finb/Bark",
		Description: "把 Gotify 收到的每条通知实时镜像推送到 Bark（iOS），支持自建 bark-server、端到端加密、优先级过滤、铃声/分组/图标等完整 Bark 参数。",
		License:     "GPL-3.0-or-later",
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
