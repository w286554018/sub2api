package handler

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// cyberBlockRecordingCache 保留真实缓存实现，并记录封禁写入尝试。
// 它用于验证受信用户的处理不会写入会话或历史正文封禁。
type cyberBlockRecordingCache struct {
	service.GatewayCache
	service.CyberSessionBlockStore
	writes atomic.Int64
}

func (c *cyberBlockRecordingCache) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	c.writes.Add(1)
	return c.CyberSessionBlockStore.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl)
}
