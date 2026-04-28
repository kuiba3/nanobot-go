// Package web implements the web_search + web_fetch tools.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kuiba3/nanobot-go/internal/config"
	"github.com/kuiba3/nanobot-go/internal/security"
	"github.com/kuiba3/nanobot-go/internal/tools"
)

const fetchMaxBytes = 500_000
const defaultUA = "nanobot-go/0.1"

// New returns the web_search + web_fetch tools based on the supplied config.
// Both tools honor the SSRF whitelist.
func New(cfg config.WebToolConfig, w *security.Whitelist, httpClient *http.Client) []tools.Tool {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return []tools.Tool{
		&webSearch{Base: tools.Base{
			ToolName:        "web_search",
			ToolDescription: "Search the web and return a list of results (title, url, snippet).",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"query": {Type: "string"},
					"limit": {Type: "integer", Description: "max results (1-10, default 5)"},
				},
				Required: []string{"query"},
			},
		}, cfg: cfg, client: httpClient},
		&webFetch{Base: tools.Base{
			ToolName:        "web_fetch",
			ToolDescription: "Fetch a URL and return the response body (truncated to 500KB).",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"url":    {Type: "string"},
					"method": {Type: "string", Enum: []any{"GET", "POST"}},
					"body":   {Type: "string"},
				},
				Required: []string{"url"},
			},
		}, w: w, client: httpClient},
	}
}

type webSearch struct {
	tools.Base
	cfg    config.WebToolConfig
	client *http.Client
}

// Execute routes to the configured search provider. Falls back to a simple
// DuckDuckGo HTML parser when no provider is configured.
func (t *webSearch) Execute(ctx context.Context, args map[string]any) (string, error) {
	q := tools.ArgString(args, "query", "")
	if strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("query must be non-empty")
	}
	limit := tools.ArgInt(args, "limit", 5)
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	switch strings.ToLower(t.cfg.Provider) {
	case "tavily":
		return tavilySearch(ctx, t.client, t.cfg.APIKey, q, limit)
	case "brave":
		return braveSearch(ctx, t.client, t.cfg.APIKey, q, limit)
	default:
		return duckDuckGoSearch(ctx, t.client, q, limit)
	}
}

// SearchResult is the shared row shape across providers.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

func tavilySearch(ctx context.Context, c *http.Client, key, q string, limit int) (string, error) {
	if key == "" {
		return "", fmt.Errorf("tavily requires apiKey")
	}
	body := map[string]any{"api_key": key, "query": q, "max_results": limit}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("tavily parse: %w", err)
	}
	return formatResults(parsed.Results), nil
}

func braveSearch(ctx context.Context, c *http.Client, key, q string, limit int) (string, error) {
	if key == "" {
		return "", fmt.Errorf("brave requires apiKey")
	}
	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", url.QueryEscape(q), limit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Subscription-Token", key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("brave parse: %w", err)
	}
	list := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		list = append(list, SearchResult{Title: r.Title, URL: r.URL, Content: r.Description})
	}
	return formatResults(list), nil
}

func duckDuckGoSearch(ctx context.Context, c *http.Client, q string, limit int) (string, error) {
	u := fmt.Sprintf("https://duckduckgo.com/html/?q=%s", url.QueryEscape(q))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", defaultUA)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return extractDDGResults(string(data), limit), nil
}

func extractDDGResults(html string, limit int) string {
	var out []SearchResult
	i := 0
	for len(out) < limit {
		idx := strings.Index(html[i:], `class="result__a"`)
		if idx < 0 {
			break
		}
		i += idx
		hrefIdx := strings.Index(html[i:], `href="`)
		if hrefIdx < 0 {
			break
		}
		start := i + hrefIdx + len(`href="`)
		end := strings.Index(html[start:], `"`)
		if end < 0 {
			break
		}
		urlStr := html[start : start+end]
		if strings.HasPrefix(urlStr, "//") {
			urlStr = "https:" + urlStr
		}
		titleStart := strings.Index(html[start+end:], ">")
		if titleStart < 0 {
			break
		}
		titleEnd := strings.Index(html[start+end+titleStart:], "</a>")
		if titleEnd < 0 {
			break
		}
		title := strings.TrimSpace(html[start+end+titleStart+1 : start+end+titleStart+titleEnd])
		out = append(out, SearchResult{Title: title, URL: urlStr})
		i = start + end + titleStart + titleEnd
	}
	return formatResults(out)
}

func formatResults(rs []SearchResult) string {
	var b strings.Builder
	for i, r := range rs {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, shortContent(r.Content))
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortContent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "..."
	}
	return s
}

// --- fetch -----------------------------------------------------------------

type webFetch struct {
	tools.Base
	w      *security.Whitelist
	client *http.Client
}

func (t *webFetch) Execute(ctx context.Context, args map[string]any) (string, error) {
	rawURL := tools.ArgString(args, "url", "")
	if rawURL == "" {
		return "", fmt.Errorf("url must be non-empty")
	}
	if err := security.ValidateURL(ctx, rawURL, t.w); err != nil {
		return "", err
	}
	method := strings.ToUpper(tools.ArgString(args, "method", "GET"))
	var body io.Reader
	if method == "POST" {
		body = strings.NewReader(tools.ArgString(args, "body", ""))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(fetchMaxBytes+1)))
	if err != nil {
		return "", err
	}
	truncated := ""
	if len(data) > fetchMaxBytes {
		data = data[:fetchMaxBytes]
		truncated = fmt.Sprintf("\n\n[truncated at %d bytes]", fetchMaxBytes)
	}
	return fmt.Sprintf("HTTP %d\n\n%s%s", resp.StatusCode, string(data), truncated), nil
}
