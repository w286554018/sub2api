package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type GlobalModelPrice struct {
	ID                int64       `json:"id"`
	ModelPattern      string      `json:"model_pattern"`
	BillingMode       BillingMode `json:"billing_mode"`
	InputPrice        *float64    `json:"input_price,omitempty"`
	OutputPrice       *float64    `json:"output_price,omitempty"`
	CacheWritePrice   *float64    `json:"cache_write_price,omitempty"`
	CacheWrite1hPrice *float64    `json:"cache_write_1h_price,omitempty"`
	CacheReadPrice    *float64    `json:"cache_read_price,omitempty"`
	PerRequestPrice   *float64    `json:"per_request_price,omitempty"`
	Enabled           bool        `json:"enabled"`
	CreatedAt         time.Time   `json:"created_at,omitempty"`
	UpdatedAt         time.Time   `json:"updated_at,omitempty"`
}

type GlobalModelPriceInput struct {
	ModelPattern      string      `json:"model_pattern"`
	BillingMode       BillingMode `json:"billing_mode"`
	InputPrice        *float64    `json:"input_price"`
	OutputPrice       *float64    `json:"output_price"`
	CacheWritePrice   *float64    `json:"cache_write_price"`
	CacheWrite1hPrice *float64    `json:"cache_write_1h_price"`
	CacheReadPrice    *float64    `json:"cache_read_price"`
	PerRequestPrice   *float64    `json:"per_request_price"`
}

type GlobalModelPricingRepository interface {
	ListEnabled(ctx context.Context) ([]GlobalModelPrice, error)
	ListAll(ctx context.Context) ([]GlobalModelPrice, error)
	Create(ctx context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error)
	GetByID(ctx context.Context, id int64) (*GlobalModelPrice, error)
	Update(ctx context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error)
	Delete(ctx context.Context, id int64) error
	SetEnabled(ctx context.Context, id int64, enabled bool) (*GlobalModelPrice, error)
}

type GlobalModelPricingCachePubSub interface {
	NotifyUpdate(ctx context.Context) error
	SubscribeUpdates(ctx context.Context, handler func())
}

var ErrGlobalModelPricingNotFound = errors.New("global model pricing not found")
var ErrGlobalModelPricingDuplicate = errors.New("global model pricing already exists for this model")

const globalModelPricingSnapshotTTL = 60 * time.Second

type GlobalModelPricingService struct {
	repo        GlobalModelPricingRepository
	cachePubSub GlobalModelPricingCachePubSub

	mu         sync.RWMutex
	snapshot   []GlobalModelPrice
	loadedAt   time.Time
	generation uint64
}

func NewGlobalModelPricingService(repo GlobalModelPricingRepository, cachePubSub GlobalModelPricingCachePubSub) *GlobalModelPricingService {
	svc := &GlobalModelPricingService{repo: repo, cachePubSub: cachePubSub}
	if cachePubSub != nil {
		cachePubSub.SubscribeUpdates(context.Background(), svc.Invalidate)
	}
	return svc
}

func ValidateGlobalModelPriceInput(in GlobalModelPriceInput) error {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	if pattern == "" {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_REQUIRED", "model_pattern must not be empty")
	}
	if len([]rune(pattern)) > 200 {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_TOO_LONG", "model_pattern must not exceed 200 runes")
	}
	if strings.Contains(pattern, " ") {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "model_pattern must not contain spaces")
	}
	if stars := strings.Count(pattern, "*"); stars > 1 || (stars == 1 && !strings.HasSuffix(pattern, "*")) {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "only a single trailing * wildcard is supported")
	}
	if core := strings.TrimSuffix(pattern, "*"); core == "" {
		return infraerrors.BadRequest("GLOBAL_PRICING_PATTERN_INVALID", "model_pattern must contain a model name")
	}
	mode := in.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	if !mode.IsValid() || mode == "" {
		return infraerrors.BadRequest("GLOBAL_PRICING_MODE_INVALID", "unsupported billing_mode "+string(in.BillingMode))
	}
	for name, v := range map[string]*float64{
		"input_price":          in.InputPrice,
		"output_price":         in.OutputPrice,
		"cache_write_price":    in.CacheWritePrice,
		"cache_write_1h_price": in.CacheWrite1hPrice,
		"cache_read_price":     in.CacheReadPrice,
		"per_request_price":    in.PerRequestPrice,
	} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
			return infraerrors.BadRequest("GLOBAL_PRICING_PRICE_INVALID", name+" must be a finite non-negative number")
		}
	}
	if mode == BillingModeToken {
		if in.InputPrice == nil || in.OutputPrice == nil {
			return infraerrors.BadRequest("GLOBAL_PRICING_TOKEN_PRICE_REQUIRED", "token billing requires input_price and output_price")
		}
		if in.PerRequestPrice != nil {
			return infraerrors.BadRequest("GLOBAL_PRICING_TOKEN_PRICE_INVALID", "token billing does not accept per_request_price")
		}
		return nil
	}
	if in.PerRequestPrice == nil {
		return infraerrors.BadRequest("GLOBAL_PRICING_REQUEST_PRICE_REQUIRED", "non-token billing requires per_request_price")
	}
	if in.InputPrice != nil || in.OutputPrice != nil || in.CacheWritePrice != nil || in.CacheWrite1hPrice != nil || in.CacheReadPrice != nil {
		return infraerrors.BadRequest("GLOBAL_PRICING_REQUEST_PRICE_INVALID", "non-token billing does not accept token prices")
	}
	return nil
}

func (s *GlobalModelPricingService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.loadedAt = time.Time{}
	s.generation++
	s.mu.Unlock()
}

func (s *GlobalModelPricingService) snapshotPrices(ctx context.Context) []GlobalModelPrice {
	if s == nil || s.repo == nil {
		return nil
	}
	now := time.Now()
	s.mu.RLock()
	if s.snapshot != nil && now.Sub(s.loadedAt) < globalModelPricingSnapshotTTL {
		rows := append([]GlobalModelPrice(nil), s.snapshot...)
		s.mu.RUnlock()
		return rows
	}
	stale := append([]GlobalModelPrice(nil), s.snapshot...)
	generation := s.generation
	s.mu.RUnlock()

	rows, err := s.repo.ListEnabled(ctx)
	if err != nil {
		slog.Warn("failed to load global model pricing snapshot", "error", err)
		return stale
	}
	s.mu.Lock()
	if s.generation != generation {
		current := append([]GlobalModelPrice(nil), s.snapshot...)
		s.mu.Unlock()
		return current
	}
	s.snapshot = append([]GlobalModelPrice(nil), rows...)
	s.loadedAt = now
	s.mu.Unlock()
	return rows
}

func (s *GlobalModelPricingService) Match(ctx context.Context, model string) *ChannelModelPricing {
	rows := s.snapshotPrices(ctx)
	if len(rows) == 0 {
		return nil
	}
	if hit := matchGlobalRows(rows, normalizeChannelPricingModelName(model)); hit != nil {
		return hit
	}
	if normalized := normalizeKnownOpenAICodexModel(model); normalized != "" && !strings.EqualFold(strings.TrimSpace(normalized), strings.TrimSpace(model)) {
		return matchGlobalRows(rows, normalizeChannelPricingModelName(normalized))
	}
	return nil
}

func matchGlobalRows(rows []GlobalModelPrice, name string) *ChannelModelPricing {
	var wildcard *GlobalModelPrice
	var wildcardLen int
	for i := range rows {
		entry := &rows[i]
		pattern := normalizeChannelPricingModelName(strings.TrimSpace(entry.ModelPattern))
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := normalizeChannelPricingModelName(strings.TrimSuffix(pattern, "*"))
			if prefix != "" && strings.HasPrefix(name, prefix) && len(prefix) > wildcardLen {
				cp := *entry
				wildcard = &cp
				wildcardLen = len(prefix)
			}
			continue
		}
		if pattern == name {
			out := globalPriceToChannel(entry)
			return &out
		}
	}
	if wildcard == nil {
		return nil
	}
	out := globalPriceToChannel(wildcard)
	return &out
}

func globalPriceToChannel(p *GlobalModelPrice) ChannelModelPricing {
	mode := p.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	return ChannelModelPricing{
		Models:            []string{p.ModelPattern},
		BillingMode:       mode,
		InputPrice:        copyFloatPtr(p.InputPrice),
		OutputPrice:       copyFloatPtr(p.OutputPrice),
		CacheWritePrice:   copyFloatPtr(p.CacheWritePrice),
		CacheWrite1hPrice: copyFloatPtr(p.CacheWrite1hPrice),
		CacheReadPrice:    copyFloatPtr(p.CacheReadPrice),
		PerRequestPrice:   copyFloatPtr(p.PerRequestPrice),
	}
}

func copyFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func (s *GlobalModelPricingService) Get(ctx context.Context, id int64) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	out, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	return out, nil
}

func (s *GlobalModelPricingService) List(ctx context.Context) ([]GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	return s.repo.ListAll(ctx)
}

func (s *GlobalModelPricingService) Create(ctx context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	if err := ValidateGlobalModelPriceInput(in); err != nil {
		return nil, err
	}
	in.ModelPattern = normalizeGlobalModelPattern(in.ModelPattern)
	out, err := s.repo.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	s.invalidateAndNotify()
	return out, nil
}

func (s *GlobalModelPricingService) Update(ctx context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	if err := ValidateGlobalModelPriceInput(in); err != nil {
		return nil, err
	}
	in.ModelPattern = normalizeGlobalModelPattern(in.ModelPattern)
	out, err := s.repo.Update(ctx, id, in)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	s.invalidateAndNotify()
	return out, nil
}

func (s *GlobalModelPricingService) Delete(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return errors.New("global pricing service unavailable")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGlobalModelPricingNotFound
		}
		return err
	}
	s.invalidateAndNotify()
	return nil
}

func (s *GlobalModelPricingService) SetEnabled(ctx context.Context, id int64, enabled bool) (*GlobalModelPrice, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("global pricing service unavailable")
	}
	out, err := s.repo.SetEnabled(ctx, id, enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGlobalModelPricingNotFound
		}
		return nil, err
	}
	s.invalidateAndNotify()
	return out, nil
}

func (s *GlobalModelPricingService) invalidateAndNotify() {
	s.Invalidate()
	if s.cachePubSub == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.cachePubSub.NotifyUpdate(ctx); err != nil {
		slog.Warn("failed to publish global model pricing invalidation", "error", err)
	}
}

func normalizeGlobalModelPattern(pattern string) string {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if strings.HasSuffix(pattern, "*") {
		return normalizeChannelPricingModelName(strings.TrimSuffix(pattern, "*")) + "*"
	}
	return normalizeChannelPricingModelName(pattern)
}
