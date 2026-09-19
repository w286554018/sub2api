package repository

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const globalPricingCachePubSubKey = "global_model_pricing_updated"

type globalModelPricingCache struct {
	rdb *redis.Client
}

func NewGlobalModelPricingCache(rdb *redis.Client) service.GlobalModelPricingCachePubSub {
	return &globalModelPricingCache{rdb: rdb}
}

func (c *globalModelPricingCache) NotifyUpdate(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Publish(ctx, globalPricingCachePubSubKey, "refresh").Err()
}

func (c *globalModelPricingCache) SubscribeUpdates(ctx context.Context, handler func()) {
	if c == nil || c.rdb == nil || handler == nil {
		return
	}
	go func() {
		pubsub := c.rdb.Subscribe(ctx, globalPricingCachePubSubKey)
		defer func() {
			if err := pubsub.Close(); err != nil {
				slog.Warn("failed to close global pricing cache subscriber", "error", err)
			}
		}()

		messages := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messages:
				if !ok {
					slog.Warn("global pricing cache subscriber stopped", "reason", "channel_closed")
					return
				}
				if message != nil {
					handler()
				}
			}
		}
	}()
}
