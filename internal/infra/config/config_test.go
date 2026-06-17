package config

import "testing"

func TestNormalizeMCPServers(t *testing.T) {
	cfg := Default()
	cfg.MCP.Servers = []MCPServerConfig{
		{
			Name:       " tavily ",
			Enabled:    true,
			URL:        " https://mcp.example.com/mcp/ ",
			Headers:    map[string]string{" Authorization ": " Bearer token ", "empty": " "},
			ToolPrefix: " mcp_tavily ",
		},
	}

	cfg.normalize()

	server := cfg.MCP.Servers[0]
	if server.Name != "tavily" {
		t.Fatalf("unexpected name: %q", server.Name)
	}
	if server.Transport != "streamable-http" {
		t.Fatalf("unexpected transport: %q", server.Transport)
	}
	if server.URL != "https://mcp.example.com/mcp/" {
		t.Fatalf("unexpected url: %q", server.URL)
	}
	if server.TimeoutSeconds != 30 {
		t.Fatalf("unexpected timeout: %d", server.TimeoutSeconds)
	}
	if server.ToolPrefix != "mcp_tavily" {
		t.Fatalf("unexpected prefix: %q", server.ToolPrefix)
	}
	if got := server.Headers["Authorization"]; got != "Bearer token" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
	if _, ok := server.Headers["empty"]; ok {
		t.Fatalf("empty header should be removed: %#v", server.Headers)
	}
}
