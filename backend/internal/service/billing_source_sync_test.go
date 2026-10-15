//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Cached-content admission must reserve the same account-adjusted amount that
// the asynchronous billing path eventually charges, including a free account.
func TestSourceSyncCacheStorageQuoteMatchesAccountAdjustedBilling(t *testing.T) {
	for _, factor := range []float64{0, 0.5, 3} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			ctx := context.Background()
			svc, usageRepo := newGeminiCacheBillingGatewayForTest(t)
			groupID := int64(33)
			user := &User{ID: 621}
			apiKey := &APIKey{ID: 821, GroupID: &groupID, User: user,
				Group: &Group{ID: groupID, Platform: PlatformGemini, RateMultiplier: 2, Hydrated: true}}
			input := &RecordUsageInput{
				Result: &ForwardResult{RequestID: "source-sync-storage", Model: "gemini-2.5-pro",
					Usage: ClaudeUsage{InputTokens: 1000}, CacheStorageTokenHours: 24000},
				APIKey: apiKey, User: user,
				Account:   &Account{ID: 713, Platform: PlatformGemini, GroupRateMultiplier: &factor},
				PricingAt: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
			}
			want := (1000*0.00000125 + 24000*0.0000045) * 2 * factor
			quote := svc.QuoteUsageCost(ctx, input)
			require.InDelta(t, want, quote, 1e-12)
			require.NoError(t, svc.RecordUsage(ctx, input))
			require.NotNil(t, usageRepo.lastLog)
			require.InDelta(t, quote, usageRepo.lastLog.ActualCost, 1e-12)
			require.InDelta(t, 2*factor, usageRepo.lastLog.RateMultiplier, 1e-12)
		})
	}
}

func TestSourceSyncAccountMultiplierComposesWithPeakRate(t *testing.T) {
	svc := &GatewayService{}
	factor := 3.0
	when := time.Date(2026, 10, 15, 0, 30, 0, 0, time.UTC)
	key := &APIKey{GroupID: i64p(1), Group: &Group{ID: 1, RateMultiplier: 2,
		PeakRateEnabled: true, PeakStart: "00:00", PeakEnd: "01:00", PeakRateMultiplier: 5}}
	tokenRate, imageRate, pricedAt := svc.recordUsageMultipliers(context.Background(), key, nil, when, &Account{GroupRateMultiplier: &factor})
	require.Equal(t, 30.0, tokenRate)
	require.Equal(t, 6.0, imageRate)
	require.Equal(t, when, pricedAt)
}

func TestSourceSyncOpenAIGrouplessExplicitPricing(t *testing.T) {
	inputPrice, outputPrice := 0.01, 0.02
	global := NewGlobalModelPricingService(&globalPricingRepoStub{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "private-model", BillingMode: BillingModeToken,
			InputPrice: &inputPrice, OutputPrice: &outputPrice, Enabled: true},
	}}, nil)
	billing := NewBillingService(nil, nil)
	svc := &OpenAIGatewayService{billingService: billing,
		resolver: NewModelPricingResolverWithGlobal(nil, billing, global)}
	cost, err := svc.calculateOpenAIRecordUsageTokenCost(context.Background(), &APIKey{}, "private-model", 2,
		time.Time{}, UsageTokens{InputTokens: 2, OutputTokens: 3}, "", "", nil, true)
	require.NoError(t, err)
	require.InDelta(t, 0.08, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.16, cost.ActualCost, 1e-12)
}
