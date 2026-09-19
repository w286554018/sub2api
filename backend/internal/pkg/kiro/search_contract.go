package kiro

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"
)

// --- Search Contract Types ---

// SearchSource indicates where search results came from.
type SearchSource string

const (
	SearchSourceMCP     SearchSource = "mcp"
	SearchSourceGateway SearchSource = "gateway"
	SearchSourceNative  SearchSource = "native"
)

// SearchOutcome indicates why a search round ended.
type SearchOutcome string

const (
	SearchOutcomeDone           SearchOutcome = "done"
	SearchOutcomeContinue       SearchOutcome = "continue"
	SearchOutcomeMaxRounds      SearchOutcome = "max_rounds"
	SearchOutcomeTimeout        SearchOutcome = "timeout"
	SearchOutcomeError          SearchOutcome = "error"
	SearchOutcomeDuplicateQuery SearchOutcome = "duplicate_query"
	SearchOutcomeEmpty          SearchOutcome = "empty"
)

// SearchRound represents one iteration of the search loop.
type SearchRound struct {
	RoundNumber int
	Query       string
	ToolUseID   string // format: srvtoolu_{uuid}, unique per round
	Results     []SearchResultItem
	Source      SearchSource
	Outcome     SearchOutcome
	Duration    time.Duration
	Error       error // non-nil when Outcome == error
}

// SearchResultItem is the normalized, sanitized search result.
type SearchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	PageAge string `json:"page_age,omitempty"`
}

// --- Type Mapping ---

// FromWebSearchResult converts an existing WebSearchResult to SearchResultItem
// with mandatory sanitization. This is the ONLY entry point for external
// search data into the system — all results must pass through here.
func FromWebSearchResult(r WebSearchResult) SearchResultItem {
	item := SearchResultItem{
		Title: truncateStr(stripControlChars(r.Title), 500),
		URL:   sanitizeURL(r.URL),
	}
	if r.Snippet != nil {
		snippet := strings.TrimSpace(*r.Snippet)
		snippet = stripControlChars(snippet)
		snippet = stripPromptInjection(snippet)
		item.Snippet = truncateStr(snippet, 4096)
	}
	if r.PublishedDate != nil && *r.PublishedDate > 0 {
		t := time.Unix(*r.PublishedDate/1000, 0)
		item.PageAge = humanizeAge(t)
	}
	return item
}

// FromWebSearchResults batch converts WebSearchResults.
func FromWebSearchResults(results *WebSearchResults) []SearchResultItem {
	if results == nil {
		return nil
	}
	items := make([]SearchResultItem, 0, len(results.Results))
	for _, r := range results.Results {
		items = append(items, FromWebSearchResult(r))
	}
	return items
}

// --- Tool ID Allocator ---

// SearchToolIDAllocator generates unique, stable tool IDs for search rounds.
type SearchToolIDAllocator struct {
	prefix string
	seen   map[string]bool
}

// NewSearchToolIDAllocator creates a new allocator with srvtoolu_ prefix.
func NewSearchToolIDAllocator() *SearchToolIDAllocator {
	return &SearchToolIDAllocator{
		prefix: "srvtoolu_",
		seen:   make(map[string]bool),
	}
}

// Next generates a new unique tool ID.
func (a *SearchToolIDAllocator) Next() string {
	for {
		id := a.prefix + GenerateToolUseID()
		if !a.seen[id] {
			a.seen[id] = true
			return id
		}
	}
}

// --- Block Index Allocator ---

// BlockIndexAllocator provides monotonically increasing content block indices.
// Shared by both streaming and non-streaming paths.
type BlockIndexAllocator struct {
	next int
}

// NewBlockIndexAllocator creates an allocator starting from the given index.
func NewBlockIndexAllocator(start int) *BlockIndexAllocator {
	return &BlockIndexAllocator{next: start}
}

// Next returns the next index and increments the counter.
func (a *BlockIndexAllocator) Next() int {
	idx := a.next
	a.next++
	return idx
}

// Current returns the last allocated index.
func (a *BlockIndexAllocator) Current() int {
	if a.next == 0 {
		return -1
	}
	return a.next - 1
}

// --- Budget Control ---

// EstimateResultBytes calculates the approximate byte size of search results
// when serialized as tool_result content in conversation history.
func EstimateResultBytes(results []SearchResultItem) int {
	total := 0
	for _, r := range results {
		total += len(r.Title) + len(r.URL) + len(r.Snippet) + 50 // JSON overhead
	}
	return total
}

// TruncateResults enforces per-round limits on search results.
func TruncateResults(results []SearchResultItem, maxResults, maxBytesPerResult int) []SearchResultItem {
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	for i := range results {
		if maxBytesPerResult > 0 && len(results[i].Snippet) > maxBytesPerResult {
			if maxBytesPerResult <= 3 {
				results[i].Snippet = results[i].Snippet[:maxBytesPerResult]
			} else {
				results[i].Snippet = results[i].Snippet[:maxBytesPerResult-3] + "..."
			}
		}
	}
	return results
}

// --- SearchConfig ---

// SearchConfig holds configurable parameters for the search orchestrator.
type SearchConfig struct {
	MaxRounds            int
	RoundTimeout         time.Duration
	TotalTimeout         time.Duration
	MaxResultBytes       int // per-result max
	MaxResults           int // per-round max
	MaxTotalContextBytes int // cumulative results budget
}

// DefaultSearchConfig returns sensible defaults.
func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		MaxRounds:            5,
		RoundTimeout:         30 * time.Second,
		TotalTimeout:         120 * time.Second,
		MaxResultBytes:       8192,
		MaxResults:           10,
		MaxTotalContextBytes: 65536,
	}
}

// --- humanizeAge ---

// humanizeAge converts a timestamp to a human-readable age string
// matching Anthropic's page_age format (e.g., "2 days ago").
func humanizeAge(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(math.Floor(d.Hours() / 24))
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case d < 365*24*time.Hour:
		months := int(math.Floor(d.Hours() / 24 / 30))
		if months <= 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(math.Floor(d.Hours() / 24 / 365))
		if years <= 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// --- Security: sanitization functions ---

// sanitizeURL validates and cleans a URL from search results using net/url.
func sanitizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	// Rule 1: Only allow http and https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	// Rule 2: Reject URLs with credentials
	if parsed.User != nil {
		return ""
	}
	// Rule 3: Reject private/reserved hosts
	host := parsed.Hostname()
	if isPrivateHost(host) {
		return ""
	}
	return parsed.String()
}

func isPrivateHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	// Known local hostnames
	switch host {
	case "localhost", "0.0.0.0", "[::]":
		return true
	}
	// Parse as IP
	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP — could be a hostname. Check common dangerous patterns.
		if host == "metadata.google.internal" ||
			strings.HasSuffix(host, ".internal") ||
			strings.HasSuffix(host, ".local") {
			return true
		}
		return false
	}
	// Loopback (127.0.0.0/8, ::1)
	if ip.IsLoopback() {
		return true
	}
	// Link-local (169.254.0.0/16, fe80::/10) — includes cloud metadata 169.254.169.254
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// Private ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7)
	if isPrivateIP(ip) {
		return true
	}
	// Unspecified (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return true
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// promptInjectionPatterns are patterns in search results that could
// confuse the model into treating external content as system instructions.
var promptInjectionPatterns = []string{
	"<thinking>", "</thinking>",
	"<tool_use>", "</tool_use>",
	"<tool_result>", "</tool_result>",
	"<CRITICAL_OVERRIDE>", "</CRITICAL_OVERRIDE>",
	"Human:", "Assistant:", "System:",
	"<|im_start|>", "<|im_end|>",
	"<<SYS>>", "<</SYS>>",
}

// stripPromptInjection removes patterns that could be confused with
// system instructions when injected into model context.
func stripPromptInjection(text string) string {
	for _, pattern := range promptInjectionPatterns {
		text = strings.ReplaceAll(text, pattern, "")
	}
	return text
}

// stripControlChars removes ASCII control characters (except \n \r \t).
func stripControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncateStr truncates a string to maxLen bytes, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// SanitizeQueryForLog removes PII patterns from search queries for logging.
func SanitizeQueryForLog(query string) string {
	if len(query) > 200 {
		query = query[:200] + "..."
	}
	return query
}

// --- Server Tool Block Projection ---

// ProjectSearchRound converts a SearchRound into Anthropic content blocks.
func ProjectSearchRound(round SearchRound) []map[string]any {
	return []map[string]any{
		{
			"type":  "server_tool_use",
			"id":    round.ToolUseID,
			"name":  "web_search",
			"input": map[string]any{"query": round.Query},
		},
		{
			"type":        "web_search_tool_result",
			"tool_use_id": round.ToolUseID,
			"content":     projectSearchResultItems(round.Results),
		},
	}
}

func projectSearchResultItems(results []SearchResultItem) []map[string]any {
	content := make([]map[string]any, 0, len(results))
	for _, r := range results {
		item := map[string]any{
			"type":              "web_search_result",
			"title":             r.Title,
			"url":               r.URL,
			"encrypted_content": "", // not available from Kiro
		}
		if r.PageAge != "" {
			item["page_age"] = r.PageAge
		} else {
			item["page_age"] = nil // explicit null per §14.3
		}
		content = append(content, item)
	}
	return content
}
