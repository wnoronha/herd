package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	scriptTagRegex = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleTagRegex  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	newlineRegex   = regexp.MustCompile(`(?i)<(br|p|div|h[1-6]|tr|li)[^>]*>`)
	tagRegex       = regexp.MustCompile(`<[^>]+>`)
	multiSpace     = regexp.MustCompile(`[ \t]+`)
	multiNewline   = regexp.MustCompile(`\n{3,}`)
)

// buildFetchURLTool returns an agent tool for retrieving and parsing web content from HTTP/HTTPS URLs.
func buildFetchURLTool() ToolDefinition {
	return ToolDefinition{
		Name:        "fetch_url",
		Description: "Fetch content from an HTTP or HTTPS URL and return clean text or JSON content.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The complete HTTP or HTTPS URL to fetch (e.g. 'https://example.com/api/data')",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Optional maximum number of bytes to read (default: 65536, max: 524288)",
				},
			},
			"required": []string{"url"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			rawURL, _ := args["url"].(string)
			rawURL = strings.TrimSpace(rawURL)
			if rawURL == "" {
				return nil, fmt.Errorf("'url' parameter is required")
			}

			parsedURL, err := url.Parse(rawURL)
			if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				return nil, fmt.Errorf("invalid URL: must use http or https scheme")
			}

			maxBytes := int64(65536)
			if mb, ok := args["max_bytes"].(float64); ok && mb > 0 {
				maxBytes = int64(mb)
				if maxBytes > 524288 {
					maxBytes = 524288
				}
			}

			client := &http.Client{
				Timeout: 20 * time.Second,
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			req.Header.Set("User-Agent", "Herd-Agent/1.0 (Decentralized Node; +https://github.com/herd)")
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.8")

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("HTTP request failed: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			limitReader := io.LimitReader(resp.Body, maxBytes)
			bodyBytes, err := io.ReadAll(limitReader)
			if err != nil {
				return nil, fmt.Errorf("failed to read response: %w", err)
			}

			contentType := resp.Header.Get("Content-Type")
			content := string(bodyBytes)

			if strings.Contains(strings.ToLower(contentType), "html") {
				content = sanitizeHTMLContent(content)
			}

			return map[string]any{
				"url":          parsedURL.String(),
				"status_code":  resp.StatusCode,
				"content_type": contentType,
				"content":      content,
				"bytes_read":   len(bodyBytes),
			}, nil
		},
	}
}

// buildGoogleSearchTool returns an agent tool for web search grounding.
func buildGoogleSearchTool() ToolDefinition {
	return ToolDefinition{
		Name:        "google_search",
		Description: "Search the web for real-time information, documentation, and external grounding.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query keywords",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "Optional number of search results to return (default: 5, max: 10)",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return nil, fmt.Errorf("'query' parameter is required")
			}

			numResults := 5
			if n, ok := args["num_results"].(float64); ok && n > 0 {
				numResults = int(n)
				if numResults > 10 {
					numResults = 10
				}
			}

			// 1. If Google Custom Search credentials are set (GOOGLE_SEARCH_API_KEY + GOOGLE_SEARCH_CX), use Google API
			apiKey := os.Getenv("GOOGLE_SEARCH_API_KEY")
			cx := os.Getenv("GOOGLE_SEARCH_CX")
			if apiKey != "" && cx != "" {
				res, err := executeGoogleCustomSearch(ctx, query, apiKey, cx, numResults)
				if err == nil {
					return res, nil
				}
			}

			// 2. Default universal fallback search via DuckDuckGo HTML / Instant Answers
			return executeWebSearchFallback(ctx, query, numResults)
		},
	}
}

func sanitizeHTMLContent(rawHTML string) string {
	s := scriptTagRegex.ReplaceAllString(rawHTML, " ")
	s = styleTagRegex.ReplaceAllString(s, " ")
	s = newlineRegex.ReplaceAllString(s, "\n")
	s = tagRegex.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	var cleanedLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(multiSpace.ReplaceAllString(line, " "))
		if trimmed != "" {
			cleanedLines = append(cleanedLines, trimmed)
		}
	}

	result := strings.Join(cleanedLines, "\n")
	return multiNewline.ReplaceAllString(result, "\n\n")
}

type searchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func executeGoogleCustomSearch(ctx context.Context, query, apiKey, cx string, numResults int) (map[string]any, error) {
	reqURL := fmt.Sprintf("https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&num=%d",
		url.QueryEscape(apiKey),
		url.QueryEscape(cx),
		url.QueryEscape(query),
		numResults,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google custom search API returned status: %d", resp.StatusCode)
	}

	var data struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var results []searchResultItem
	for _, item := range data.Items {
		results = append(results, searchResultItem{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
		})
	}

	return map[string]any{
		"query":    query,
		"results":  results,
		"count":    len(results),
		"provider": "google_custom_search",
	}, nil
}

func executeWebSearchFallback(ctx context.Context, query string, numResults int) (map[string]any, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return executeInstantAnswerFallback(ctx, query)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return executeInstantAnswerFallback(ctx, query)
	}

	bodyStr := string(bodyBytes)
	results := parseDuckDuckGoHTML(bodyStr, numResults)
	if len(results) == 0 {
		return executeInstantAnswerFallback(ctx, query)
	}

	return map[string]any{
		"query":    query,
		"results":  results,
		"count":    len(results),
		"provider": "web_search",
	}, nil
}

var (
	reSnippet = regexp.MustCompile(`(?i)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
	reTitle   = regexp.MustCompile(`(?i)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
)

func parseDuckDuckGoHTML(content string, limit int) []searchResultItem {
	titleMatches := reTitle.FindAllStringSubmatch(content, limit*2)
	snippetMatches := reSnippet.FindAllStringSubmatch(content, limit*2)

	var results []searchResultItem
	for i := 0; i < len(titleMatches) && len(results) < limit; i++ {
		rawTitle := titleMatches[i][2]
		rawLink := titleMatches[i][1]

		actualURL := extractActualURL(rawLink)
		cleanTitle := sanitizeHTMLContent(rawTitle)

		var cleanSnippet string
		if i < len(snippetMatches) {
			cleanSnippet = sanitizeHTMLContent(snippetMatches[i][2])
		}

		if cleanTitle != "" && actualURL != "" {
			results = append(results, searchResultItem{
				Title:   cleanTitle,
				URL:     actualURL,
				Snippet: cleanSnippet,
			})
		}
	}
	return results
}

func extractActualURL(rawLink string) string {
	if strings.Contains(rawLink, "uddg=") {
		parts := strings.Split(rawLink, "uddg=")
		if len(parts) > 1 {
			target := strings.Split(parts[1], "&")[0]
			if unescaped, err := url.QueryUnescape(target); err == nil {
				return unescaped
			}
		}
	}
	if strings.HasPrefix(rawLink, "//") {
		return "https:" + rawLink
	}
	return rawLink
}

func executeInstantAnswerFallback(ctx context.Context, query string) (map[string]any, error) {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var ddgResp struct {
		Abstract    string `json:"Abstract"`
		AbstractURL string `json:"AbstractURL"`
		Heading     string `json:"Heading"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ddgResp); err != nil {
		return nil, err
	}

	var results []searchResultItem
	if ddgResp.Abstract != "" {
		results = append(results, searchResultItem{
			Title:   ddgResp.Heading,
			URL:     ddgResp.AbstractURL,
			Snippet: ddgResp.Abstract,
		})
	}

	return map[string]any{
		"query":    query,
		"results":  results,
		"count":    len(results),
		"provider": "instant_answers",
	}, nil
}
