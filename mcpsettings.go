package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

type mcpLaunch struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func mcpLaunchFor(executable, platform, flatpakID, appImage string) mcpLaunch {
	if platform == "linux" {
		if flatpakID == "com.elianiva.meiro" {
			return mcpLaunch{"flatpak", []string{"run", "--command=meiro", flatpakID, "--mcp"}}
		}
		if filepath.IsAbs(appImage) {
			executable = appImage
		}
	}
	return mcpLaunch{executable, []string{"--mcp"}}
}

func (launch mcpLaunch) config() string {
	data, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]mcpLaunch{"meiro": launch}}, "", "  ")
	return string(data)
}

func (launch mcpLaunch) setupPrompt() string {
	return fmt.Sprintf(`Connect my running Meiro desktop app to this client as a local stdio MCP server named meiro.

Meiro must be open with Settings > Desktop > AI control switched on. If I need to open Meiro, turn it on, or approve the server in this client, ask me to do it.

Use this client's standard way to add a local stdio MCP server. In Claude Code, use user scope. Adapt the config below to this client's format. Preserve the command and argument array exactly.

%s

Add or update only the meiro entry. If you cannot change the config, show me what to add and where to put it. List the tools to check the connection, then wait for me to ask before calling one. Clear upcoming tracks only if I explicitly ask, then pass confirm: true.`, launch.config())
}

func (a *app) mcpSetting(c *ui.Context) {
	ui.Column(c.Key("ai-control")).Gap(8).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
			settingLabel(c, "AI control", "Allow local MCP clients to search, control playback, edit the queue and rate tracks.").Grow(1)
			on := a.settings.MCPControl
			if m3.Switch(c, &on, "Allow AI control").Changed() {
				a.settings.MCPControl = on
				a.saveSettings()
				a.syncMCP()
			}
		})
		if a.settings.MCPControl {
			a.mcpStatus(c)
			a.mcpSetup(c)
		}
	})
}

func (a *app) mcpStatus(c *ui.Context) {
	sc := m3.Of(c).Scheme
	problem := a.mcpError
	if a.mcp != nil {
		problem = a.mcp.Failure()
	}
	if problem != "" {
		m3.Text(c, m3.BodySmall, "Could not listen for MCP clients. "+firstLine(problem)).TextColor(sc.Error).MaxLines(3)
		if m3.Button(c, m3.ButtonSpec{Label: "Retry", Kind: m3.TextOnly, Key: "mcp-retry"}).Clicked() {
			a.syncMCP()
		}
	} else if a.mcp != nil {
		clients := a.mcp.Clients()
		label := "clients"
		if clients == 1 {
			label = "client"
		}
		m3.Text(c, m3.BodySmall, fmt.Sprintf("Listening · %d connected %s", clients, label)).TextColor(sc.OnSurfaceVariant)
	}
	if !a.mcpLastAt.IsZero() {
		action := a.mcpLastTool
		if a.mcpLastFailed {
			action += " (failed)"
		}
		m3.Text(c, m3.BodySmall, "Last agent action · "+action+" · "+a.mcpLastAt.Format("15:04:05")).TextColor(sc.OnSurfaceVariant)
	}
}

func (a *app) mcpSetup(c *ui.Context) {
	executable, err := os.Executable()
	if err != nil {
		m3.Text(c, m3.BodySmall, "Could not find Meiro's executable for setup.").TextColor(m3.Of(c).Scheme.Error)
		return
	}
	launch := mcpLaunchFor(executable, runtime.GOOS, os.Getenv("FLATPAK_ID"), os.Getenv("APPIMAGE"))
	sc := m3.Of(c).Scheme
	m3.Text(c, m3.BodySmall, "Paste the setup prompt into your agent. Keep Meiro open while you connect.").TextColor(sc.OnSurfaceVariant)
	ui.Row(c).Gap(8).Children(func() {
		if m3.Button(c, m3.ButtonSpec{Label: "Copy setup prompt", Icon: m3.IconCopy, Kind: m3.Tonal, Key: "mcp-prompt"}).Clicked() {
			c.WriteClipboard(launch.setupPrompt())
			c.Toast("Setup prompt copied")
		}
		if m3.Button(c, m3.ButtonSpec{Label: "Advanced", Icon: advancedIcon(a.mcpAdvanced), Kind: m3.TextOnly, Key: "mcp-advanced"}).Clicked() {
			a.mcpAdvanced = !a.mcpAdvanced
		}
	})
	if a.mcpAdvanced {
		config := launch.config()
		m3.Text(c, m3.BodySmall, "Add meiro as a local stdio server in your client's MCP settings.").TextColor(sc.OnSurfaceVariant)
		if m3.Button(c, m3.ButtonSpec{Label: "Copy JSON config", Icon: m3.IconCopy, Kind: m3.Tonal, Key: "mcp-json"}).Clicked() {
			c.WriteClipboard(config)
			c.Toast("JSON config copied")
		}
		ui.Column(c.Key("mcp-config")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest).Children(func() {
			m3.Text(c, m3.BodyMedium, config).TextColor(sc.OnSurface).Selectable()
		})
	}
}
