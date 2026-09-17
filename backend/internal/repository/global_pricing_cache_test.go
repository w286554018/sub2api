package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGlobalPricingCachePubSub(t *testing.T) {
	redisServer := miniredis.RunT(t)
	publisherRedis := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	subscriberRedis := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() {
		_ = publisherRedis.Close()
		_ = subscriberRedis.Close()
	})

	publisher := NewGlobalModelPricingCache(publisherRedis)
	subscriber := NewGlobalModelPricingCache(subscriberRedis)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	received := make(chan struct{}, 1)
	subscriber.SubscribeUpdates(ctx, func() {
		select {
		case received <- struct{}{}:
		default:
		}
	})

	require.Eventually(t, func() bool {
		return redisServer.PubSubNumSub(globalPricingCachePubSubKey)[globalPricingCachePubSubKey] == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, publisher.NotifyUpdate(context.Background()))

	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("global pricing cache invalidation notification was not received")
	}
}
