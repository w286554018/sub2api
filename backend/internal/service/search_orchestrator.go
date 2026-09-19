package service

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// --- Feature Flag ---

// KiroWebSearchModelLoopEnabled returns true if the model-driven search
// loop is enabled via SUB2API_KIRO_WEBSEARCH_MODEL_LOOP.
func KiroWebSearchModelLoopEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_WEBSEARCH_MODEL_LOOP")))
	return v == "true" || v == "1" || v == "yes"
}

// --- Configurable Parameters ---

func kiroSearchConfig() kiropkg.SearchConfig {
	cfg := kiropkg.DefaultSearchConfig()

	if v, err := strconv.Atoi(os.Getenv("SUB2API_KIRO_MAX_SEARCH_ROUNDS")); err == nil && v > 0 {
		cfg.MaxRounds = v
	}
	if v, err := time.ParseDuration(os.Getenv("SUB2API_KIRO_SEARCH_ROUND_TIMEOUT")); err == nil && v > 0 {
		cfg.RoundTimeout = v
	}
	if v, err := time.ParseDuration(os.Getenv("SUB2API_KIRO_SEARCH_TOTAL_TIMEOUT")); err == nil && v > 0 {
		cfg.TotalTimeout = v
	}
	if v, err := strconv.Atoi(os.Getenv("SUB2API_KIRO_MAX_SEARCH_RESULT_BYTES")); err == nil && v > 0 {
		cfg.MaxResultBytes = v
	}
	if v, err := strconv.Atoi(os.Getenv("SUB2API_KIRO_MAX_SEARCH_RESULTS")); err == nil && v > 0 {
		cfg.MaxResults = v
	}
	if v, err := strconv.Atoi(os.Getenv("SUB2API_KIRO_MAX_SEARCH_CONTEXT_BYTES")); err == nil && v > 0 {
		cfg.MaxTotalContextBytes = v
	}
	return cfg
}

// --- Search Orchestrator ---

// kiroSearchOrchestrator drives a model-participated multi-round search loop.
// It wraps the existing GatewayService MCP/upstream methods with:
// - duplicate query detection
// - configurable max rounds / timeouts
// - cumulative context budget
// - fallback from MCP to gateway search
type kiroSearchOrchestrator struct {
	svc         *GatewayService
	cfg         kiropkg.SearchConfig
	idAllocator *kiropkg.SearchToolIDAllocator
}

func newKiroSearchOrchestrator(svc *GatewayService) *kiroSearchOrchestrator {
	return &kiroSearchOrchestrator{
		svc:         svc,
		cfg:         kiroSearchConfig(),
		idAllocator: kiropkg.NewSearchToolIDAllocator(),
	}
}

// executeSearchRound runs one search round: MCP call with gateway fallback.
func (o *kiroSearchOrchestrator) executeSearchRound(
	ctx context.Context, account *Account, token, query string,
) ([]kiropkg.SearchResultItem, string, error) {
	roundCtx, cancel := context.WithTimeout(ctx, o.cfg.RoundTimeout)
	defer cancel()

	start := time.Now()
	results, nextToken, err := o.svc.callKiroWebSearchMCP(roundCtx, account, token, query)
	RecordSearchLatency(time.Since(start))

	if err == nil && results != nil {
		RecordMCPCall(true)
		items := kiropkg.FromWebSearchResults(results)
		items = kiropkg.TruncateResults(items, o.cfg.MaxResults, o.cfg.MaxResultBytes)
		return items, nextToken, nil
	}

	// MCP failed — log and continue (no gateway fallback yet in Kiro path)
	RecordMCPCall(false)
	log.Printf("[kiro] MCP search failed for query %q: %v", kiropkg.SanitizeQueryForLog(query), err)
	return nil, nextToken, err
}

// runNonStreaming executes the model-driven search loop for non-streaming requests.
// Returns completed rounds and the final model response body.
func (o *kiroSearchOrchestrator) runNonStreaming(
	ctx context.Context,
	account *Account, group *Group,
	anthropicBody []byte,
	mappedModel, requestModel, token string,
	headers http.Header,
) ([]kiropkg.SearchRound, []byte, error) {
	totalCtx, cancel := context.WithTimeout(ctx, o.cfg.TotalTimeout)
	defer cancel()

	currentBody := anthropicBody
	var rounds []kiropkg.SearchRound
	seenQueries := make(map[string]bool)
	var totalResultBytes int
	inputTokens := estimateKiroInputTokens(ctx, anthropicBody)

	for i := 0; i < o.cfg.MaxRounds; i++ {
		// Check context
		if err := totalCtx.Err(); err != nil {
			if len(rounds) > 0 {
				rounds[len(rounds)-1].Outcome = kiropkg.SearchOutcomeTimeout
			}
			break
		}

		// Extract query from current body
		query := kiropkg.ExtractSearchQuery(currentBody)
		if query == "" {
			break // model didn't request search
		}

		// Duplicate query detection
		if seenQueries[query] {
			rounds = append(rounds, kiropkg.SearchRound{
				RoundNumber: i,
				Query:       query,
				ToolUseID:   o.idAllocator.Next(),
				Outcome:     kiropkg.SearchOutcomeDuplicateQuery,
			})
			log.Printf("[kiro] duplicate search query detected: %q", kiropkg.SanitizeQueryForLog(query))
			break
		}
		seenQueries[query] = true

		toolID := o.idAllocator.Next()
		start := time.Now()

		// Execute search
		results, nextToken, err := o.executeSearchRound(totalCtx, account, token, query)
		if nextToken != "" {
			token = nextToken
		}

		round := kiropkg.SearchRound{
			RoundNumber: i,
			Query:       query,
			ToolUseID:   toolID,
			Source:      kiropkg.SearchSourceMCP,
			Duration:    time.Since(start),
		}

		if err != nil {
			round.Outcome = kiropkg.SearchOutcomeError
			round.Error = err
			round.Results = nil
			rounds = append(rounds, round)
			break // stop loop on error, propagate below
		}

		if results == nil {
			results = []kiropkg.SearchResultItem{}
		}
		round.Results = results

		// Check cumulative context budget
		roundBytes := kiropkg.EstimateResultBytes(results)
		if totalResultBytes+roundBytes > o.cfg.MaxTotalContextBytes {
			round.Outcome = kiropkg.SearchOutcomeDone
			rounds = append(rounds, round)
			log.Printf("[kiro] search context budget exhausted at round %d (%d bytes)", i, totalResultBytes+roundBytes)
			break
		}
		totalResultBytes += roundBytes

		// Convert results back to WebSearchResults for injection
		webResults := toWebSearchResults(results)
		currentBody, err = kiropkg.InjectToolResultsClaude(currentBody, toolID, query, webResults)
		if err != nil {
			round.Outcome = kiropkg.SearchOutcomeError
			round.Error = fmt.Errorf("inject tool results: %w", err)
			rounds = append(rounds, round)
			break
		}

		// Call model with injected results
		resp, _, upstreamErr := o.svc.executeKiroUpstream(totalCtx, account, currentBody, mappedModel, requestModel, token, headers)
		if upstreamErr != nil {
			round.Outcome = kiropkg.SearchOutcomeError
			round.Error = upstreamErr
			rounds = append(rounds, round)
			break
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			round.Outcome = kiropkg.SearchOutcomeError
			round.Error = &kiroWebSearchHTTPError{Response: resp}
			rounds = append(rounds, round)
			break
		}

		parseResult, parseErr := func() (*kiropkg.ParseResult, error) {
			defer func() { _ = resp.Body.Close() }()
			return kiropkg.ParseNonStreamingEventStreamWithContext(resp.Body, requestModel, kiropkg.KiroRequestContext{
				EstimatedInputTokens: inputTokens,
			})
		}()
		if parseErr != nil {
			round.Outcome = kiropkg.SearchOutcomeError
			round.Error = parseErr
			rounds = append(rounds, round)
			break
		}

		// Check if model wants another search
		_, nextQuery, hasNext := kiropkg.ExtractWebSearchToolUseFromResponse(parseResult.ResponseBody)
		if !hasNext || nextQuery == "" {
			round.Outcome = kiropkg.SearchOutcomeDone
			rounds = append(rounds, round)
			currentBody = parseResult.ResponseBody
			break
		}
		if i+1 >= o.cfg.MaxRounds {
			round.Outcome = kiropkg.SearchOutcomeMaxRounds
			rounds = append(rounds, round)
			currentBody = parseResult.ResponseBody
			break
		}

		round.Outcome = kiropkg.SearchOutcomeContinue
		rounds = append(rounds, round)
		currentBody = parseResult.ResponseBody
	}

	// If last round is still "continue", mark as max_rounds
	if len(rounds) > 0 && rounds[len(rounds)-1].Outcome == kiropkg.SearchOutcomeContinue {
		rounds[len(rounds)-1].Outcome = kiropkg.SearchOutcomeMaxRounds
	}

	// Propagate last error if any round failed
	var lastErr error
	if len(rounds) > 0 {
		lastRound := rounds[len(rounds)-1]
		if lastRound.Outcome == kiropkg.SearchOutcomeError && lastRound.Error != nil {
			lastErr = lastRound.Error
		}
	}

	return rounds, currentBody, lastErr
}

// --- Helper ---

// toWebSearchResults converts SearchResultItems back to WebSearchResults
// for compatibility with existing InjectToolResultsClaude.
func toWebSearchResults(items []kiropkg.SearchResultItem) *kiropkg.WebSearchResults {
	if len(items) == 0 {
		return nil
	}
	results := make([]kiropkg.WebSearchResult, 0, len(items))
	for _, item := range items {
		snippet := item.Snippet
		results = append(results, kiropkg.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: &snippet,
		})
	}
	return &kiropkg.WebSearchResults{Results: results}
}
