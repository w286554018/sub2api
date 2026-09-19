package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type globalPricingRepoStub struct {
	rows        []GlobalModelPrice
	listErr     error
	listHook    func()
	updates     []GlobalModelPriceInput
	setEnabled  []bool
	listEnabled int
}

func (s *globalPricingRepoStub) ListEnabled(context.Context) ([]GlobalModelPrice, error) {
	s.listEnabled++
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := append([]GlobalModelPrice(nil), s.rows...)
	if s.listHook != nil {
		s.listHook()
	}
	return out, nil
}

func (s *globalPricingRepoStub) ListAll(context.Context) ([]GlobalModelPrice, error) {
	return append([]GlobalModelPrice(nil), s.rows...), nil
}

func (s *globalPricingRepoStub) Create(_ context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	out := GlobalModelPrice{ID: int64(len(s.rows) + 1), ModelPattern: in.ModelPattern, BillingMode: in.BillingMode,
		InputPrice: in.InputPrice, OutputPrice: in.OutputPrice, CacheWritePrice: in.CacheWritePrice,
		CacheWrite1hPrice: in.CacheWrite1hPrice, CacheReadPrice: in.CacheReadPrice,
		PerRequestPrice: in.PerRequestPrice, Enabled: true}
	s.rows = append(s.rows, out)
	return &out, nil
}

func (s *globalPricingRepoStub) GetByID(_ context.Context, id int64) (*GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *globalPricingRepoStub) Update(_ context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	s.updates = append(s.updates, in)
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].ModelPattern = in.ModelPattern
			s.rows[i].BillingMode = in.BillingMode
			s.rows[i].InputPrice = in.InputPrice
			s.rows[i].OutputPrice = in.OutputPrice
			s.rows[i].PerRequestPrice = in.PerRequestPrice
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *globalPricingRepoStub) Delete(_ context.Context, id int64) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *globalPricingRepoStub) SetEnabled(_ context.Context, id int64, enabled bool) (*GlobalModelPrice, error) {
	s.setEnabled = append(s.setEnabled, enabled)
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Enabled = enabled
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

type globalPricingPubSubStub struct {
	notifyErr error
	notifyN   int
	handler   func()
}

func (s *globalPricingPubSubStub) NotifyUpdate(context.Context) error {
	s.notifyN++
	return s.notifyErr
}

func (s *globalPricingPubSubStub) SubscribeUpdates(_ context.Context, handler func()) {
	s.handler = handler
}

func gptr(v float64) *float64 { return &v }

func TestValidateGlobalModelPriceInputStrict(t *testing.T) {
	valid := []GlobalModelPriceInput{
		{ModelPattern: "GPT-5", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2)},
		{ModelPattern: "claude-3.5*", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), CacheReadPrice: gptr(0.1)},
		{ModelPattern: "imagen-*", BillingMode: BillingModeImage, PerRequestPrice: gptr(0.01)},
		{ModelPattern: "sora-*", BillingMode: BillingModeVideo, PerRequestPrice: gptr(0)},
	}
	for i, in := range valid {
		require.NoError(t, ValidateGlobalModelPriceInput(in), "valid case %d", i)
	}

	bad := []GlobalModelPriceInput{
		{ModelPattern: ""},
		{ModelPattern: "*"},
		{ModelPattern: "*foo"},
		{ModelPattern: "a*b"},
		{ModelPattern: "has space", InputPrice: gptr(1), OutputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: "hourly", InputPrice: gptr(1), OutputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: BillingModeToken, InputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), PerRequestPrice: gptr(3)},
		{ModelPattern: "x", BillingMode: BillingModeImage, PerRequestPrice: nil},
		{ModelPattern: "x", BillingMode: BillingModeImage, PerRequestPrice: gptr(1), InputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: BillingModeToken, InputPrice: gptr(math.NaN()), OutputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: BillingModeToken, InputPrice: gptr(math.Inf(1)), OutputPrice: gptr(1)},
		{ModelPattern: "x", BillingMode: BillingModeToken, InputPrice: gptr(-1), OutputPrice: gptr(1)},
	}
	for i, in := range bad {
		require.Error(t, ValidateGlobalModelPriceInput(in), "bad case %d", i)
	}
}

func TestGlobalModelPricingMatchExactAndLongestPrefix(t *testing.T) {
	svc := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), Enabled: true},
		{ID: 2, ModelPattern: "gpt-*", BillingMode: BillingModeToken, InputPrice: gptr(5), OutputPrice: gptr(6), Enabled: true},
		{ID: 3, ModelPattern: "gpt-5-*", BillingMode: BillingModeToken, InputPrice: gptr(7), OutputPrice: gptr(8), Enabled: true},
		{ID: 4, ModelPattern: "claude-3-5*", BillingMode: BillingModeToken, InputPrice: gptr(9), OutputPrice: gptr(10), Enabled: true},
	}}, nil)

	hit := svc.Match(context.Background(), "GPT-5")
	require.NotNil(t, hit)
	require.Equal(t, 1.0, *hit.InputPrice)

	hit = svc.Match(context.Background(), "gpt-5-turbo")
	require.NotNil(t, hit)
	require.Equal(t, 7.0, *hit.InputPrice)

	hit = svc.Match(context.Background(), "claude-3.5-sonnet")
	require.NotNil(t, hit)
	require.Equal(t, 9.0, *hit.InputPrice)

	require.Nil(t, svc.Match(context.Background(), "gemini-pro"))
	var nilSvc *GlobalModelPricingService
	require.Nil(t, nilSvc.Match(context.Background(), "gpt-5"))
}

func TestGlobalModelPricingKeepsStaleSnapshotOnReloadFailure(t *testing.T) {
	repo := &globalPricingRepoStub{rows: []GlobalModelPrice{{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), Enabled: true}}}
	svc := NewGlobalModelPricingService(repo, nil)
	require.NotNil(t, svc.Match(context.Background(), "gpt-5"))
	repo.rows = nil
	repo.listErr = errors.New("db down")
	svc.Invalidate()

	hit := svc.Match(context.Background(), "gpt-5")
	require.NotNil(t, hit)
	require.Equal(t, 1.0, *hit.InputPrice)
}

func TestGlobalModelPricingInvalidatePreventsStaleReloadCommit(t *testing.T) {
	repo := &globalPricingRepoStub{rows: []GlobalModelPrice{{ID: 1, ModelPattern: "gpt-old", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), Enabled: true}}}
	svc := NewGlobalModelPricingService(repo, nil)
	repo.listHook = func() {
		repo.listHook = nil
		repo.rows = []GlobalModelPrice{{ID: 2, ModelPattern: "gpt-new", BillingMode: BillingModeToken, InputPrice: gptr(3), OutputPrice: gptr(4), Enabled: true}}
		svc.Invalidate()
	}

	require.Nil(t, svc.Match(context.Background(), "gpt-old"))
	hit := svc.Match(context.Background(), "gpt-new")
	require.NotNil(t, hit)
	require.Equal(t, 3.0, *hit.InputPrice)
	require.Equal(t, 2, repo.listEnabled)
}

func TestGlobalModelPricingWritesNormalizeInvalidateAndNotify(t *testing.T) {
	repo := &globalPricingRepoStub{rows: []GlobalModelPrice{{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken, InputPrice: gptr(1), OutputPrice: gptr(2), Enabled: false}}}
	pubsub := &globalPricingPubSubStub{notifyErr: errors.New("redis down")}
	svc := NewGlobalModelPricingService(repo, pubsub)
	require.NotNil(t, pubsub.handler)

	out, err := svc.Update(context.Background(), 1, GlobalModelPriceInput{ModelPattern: "GPT-5", InputPrice: gptr(3), OutputPrice: gptr(4)})
	require.NoError(t, err)
	require.False(t, out.Enabled)
	require.Equal(t, "gpt-5", repo.updates[0].ModelPattern)
	require.Equal(t, 1, pubsub.notifyN)

	out, err = svc.SetEnabled(context.Background(), 1, true)
	require.NoError(t, err)
	require.True(t, out.Enabled)
	require.Equal(t, []bool{true}, repo.setEnabled)
}

func TestModelPricingResolverGlobalOverridesLegacyEffectiveToken(t *testing.T) {
	groupInput, groupOutput, globalInput, cacheRead := 0.000001, 0.000002, 0.00001, 0.0000005
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken, InputPrice: &globalInput, OutputPrice: gptr(0.00002), CacheReadPrice: &cacheRead, Enabled: true},
	}}, nil)
	r := NewModelPricingResolverWithGlobal(nil, &BillingService{}, global)

	resolved := r.Resolve(context.Background(), PricingInput{
		Model: "gpt-5",
		Group: &Group{ID: 9, ModelPricing: []ChannelModelPricing{{
			Models: []string{"gpt-5"}, InputPrice: &groupInput, OutputPrice: &groupOutput, CacheWritePrice: gptr(0.000003),
		}}},
	})

	require.Equal(t, PricingSourceGlobal, resolved.Source)
	require.Equal(t, BillingModeToken, resolved.Mode)
	require.NotNil(t, resolved.BasePricing)
	require.Equal(t, globalInput, resolved.BasePricing.InputPricePerToken)
	require.Equal(t, 0.00002, resolved.BasePricing.OutputPricePerToken)
	require.Equal(t, 0.000003, resolved.BasePricing.CacheCreationPricePerToken)
	require.Equal(t, cacheRead, resolved.BasePricing.CacheReadPricePerToken)
}

func TestModelPricingResolverGlobalPerRequestOverridesGroup(t *testing.T) {
	perRequest := 0.07
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "sora-*", BillingMode: BillingModeVideo, PerRequestPrice: &perRequest, Enabled: true},
	}}, nil)
	r := NewModelPricingResolverWithGlobal(nil, &BillingService{}, global)

	resolved := r.Resolve(context.Background(), PricingInput{Model: "sora-large", Group: &Group{ID: 9}})
	require.Equal(t, PricingSourceGlobal, resolved.Source)
	require.Equal(t, BillingModeVideo, resolved.Mode)
	require.Equal(t, perRequest, resolved.DefaultPerRequestPrice)
}

func TestCalculateTokenCostForRequestGrouplessUsesGlobal(t *testing.T) {
	inputPrice, outputPrice := 0.01, 0.02
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "private-model", BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice, Enabled: true},
	}}, nil)
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolverWithGlobal(nil, billing, global)

	cost, err := billing.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx:            context.Background(),
		Model:          "private-model",
		Tokens:         UsageTokens{InputTokens: 2, OutputTokens: 3},
		RateMultiplier: 1,
		Resolver:       resolver,
	})

	require.NoError(t, err)
	require.Equal(t, string(BillingModeToken), cost.BillingMode)
	require.InDelta(t, 0.08, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.08, cost.ActualCost, 1e-10)
}

func TestGatewayImageCostGlobalTokenUsesReportedTokens(t *testing.T) {
	inputPrice, outputPrice := 0.01, 0.02
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "private-image-model", BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice, Enabled: true},
	}}, nil)
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolverWithGlobal(nil, billing, global)
	gateway := &GatewayService{billingService: billing, resolver: resolver}

	cost := gateway.calculateImageCost(context.Background(), &ForwardResult{
		ImageCount: 1,
		ImageSize:  "1K",
		Usage:      ClaudeUsage{InputTokens: 2, OutputTokens: 3},
	}, &APIKey{}, "private-image-model", 1, PlatformOpenAI)

	require.Equal(t, string(BillingModeToken), cost.BillingMode)
	require.InDelta(t, 0.08, cost.TotalCost, 1e-10)
}

func TestOpenAIVideoCostGlobalPerRequestOverridesGroupVideoPrice(t *testing.T) {
	globalPrice, groupPrice := 0.7, 0.1
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "sora-*", BillingMode: BillingModePerRequest, PerRequestPrice: &globalPrice, Enabled: true},
	}}, nil)
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolverWithGlobal(nil, billing, global)
	gateway := &OpenAIGatewayService{billingService: billing, resolver: resolver}
	group := &Group{ID: 9, VideoPrice480P: &groupPrice}

	cost := gateway.calculateOpenAIVideoCost(context.Background(), "sora-test", &APIKey{GroupID: &group.ID, Group: group}, &OpenAIForwardResult{
		VideoCount:           2,
		VideoResolution:      "480p",
		VideoDurationSeconds: 5,
	}, 1)

	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
	require.InDelta(t, 1.4, cost.TotalCost, 1e-10)
}

func TestModelPricingResolverWithoutGlobalKeepsLegacy(t *testing.T) {
	r := NewModelPricingResolver(nil, &BillingService{})
	resolved := r.Resolve(context.Background(), PricingInput{Model: "gpt-5"})
	require.NotEqual(t, PricingSourceGlobal, resolved.Source)
}
