//go:build unit

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type subscriptionOwnerRepoStub struct {
	service.UserSubscriptionRepository
	subscription *service.UserSubscription
}

func (s *subscriptionOwnerRepoStub) GetByID(_ context.Context, id int64) (*service.UserSubscription, error) {
	if s.subscription == nil || s.subscription.ID != id {
		return nil, service.ErrSubscriptionNotFound
	}
	copy := *s.subscription
	return &copy, nil
}

func (s *subscriptionOwnerRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*service.UserSubscription, error) {
	return s.GetByID(ctx, id)
}

func TestContentModerationUnban_BlocksPrivilegedTarget(t *testing.T) {
	adminService := newStubAdminService()
	adminService.authorizeUserMutationErr = service.ErrInsufficientPerms
	h := &ContentModerationHandler{adminService: adminService}
	c, recorder := privilegedMutationRequest(t, http.MethodPost, "")
	c.Params = []gin.Param{{Key: "user_id", Value: "42"}}

	h.UnbanUser(c)

	requirePrivilegedMutationDenied(t, recorder, adminService, 42)
}

func TestAffiliateMutations_BlockPrivilegedTargets(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		params []gin.Param
		invoke func(*AffiliateHandler, *gin.Context)
		users  []int64
	}{
		{
			name:   "update user settings",
			method: http.MethodPut,
			body:   `{"aff_code":"protected"}`,
			params: []gin.Param{{Key: "user_id", Value: "42"}},
			invoke: func(h *AffiliateHandler, c *gin.Context) { h.UpdateUserSettings(c) },
			users:  []int64{42},
		},
		{
			name:   "clear user settings",
			method: http.MethodDelete,
			params: []gin.Param{{Key: "user_id", Value: "42"}},
			invoke: func(h *AffiliateHandler, c *gin.Context) { h.ClearUserSettings(c) },
			users:  []int64{42},
		},
		{
			name:   "batch rate",
			method: http.MethodPost,
			body:   `{"user_ids":[41,42],"clear":true}`,
			invoke: func(h *AffiliateHandler, c *gin.Context) { h.BatchSetRate(c) },
			users:  []int64{41, 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminService := newStubAdminService()
			adminService.authorizeUserMutationErr = service.ErrInsufficientPerms
			h := &AffiliateHandler{adminService: adminService}
			c, recorder := privilegedMutationRequest(t, tt.method, tt.body)
			c.Params = tt.params

			tt.invoke(h, c)

			requirePrivilegedMutationDenied(t, recorder, adminService, tt.users...)
		})
	}
}

func TestSubscriptionAssignments_BlockPrivilegedTargets(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		invoke func(*SubscriptionHandler, *gin.Context)
		users  []int64
	}{
		{
			name:   "assign",
			body:   `{"user_id":42,"group_id":3}`,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.Assign(c) },
			users:  []int64{42},
		},
		{
			name:   "bulk assign",
			body:   `{"user_ids":[41,42],"group_id":3}`,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.BulkAssign(c) },
			users:  []int64{41, 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminService := newStubAdminService()
			adminService.authorizeUserMutationErr = service.ErrInsufficientPerms
			h := &SubscriptionHandler{adminService: adminService}
			c, recorder := privilegedMutationRequest(t, http.MethodPost, tt.body)

			tt.invoke(h, c)

			requirePrivilegedMutationDenied(t, recorder, adminService, tt.users...)
		})
	}
}

func TestSubscriptionOwnerMutations_BlockPrivilegedTarget(t *testing.T) {
	repo := &subscriptionOwnerRepoStub{
		subscription: &service.UserSubscription{ID: 99, UserID: 42, GroupID: 3},
	}
	subscriptionService := service.NewSubscriptionService(nil, repo, nil, nil, nil)
	tests := []struct {
		name   string
		method string
		body   string
		invoke func(*SubscriptionHandler, *gin.Context)
	}{
		{
			name:   "extend",
			method: http.MethodPost,
			body:   `{"days":1}`,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.Extend(c) },
		},
		{
			name:   "reset quota",
			method: http.MethodPost,
			body:   `{"daily":true}`,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.ResetQuota(c) },
		},
		{
			name:   "revoke",
			method: http.MethodPost,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.Revoke(c) },
		},
		{
			name:   "restore",
			method: http.MethodPost,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.Restore(c) },
		},
		{
			name:   "bulk revoke",
			method: http.MethodPost,
			body:   `{"subscription_ids":[99],"action":"revoke"}`,
			invoke: func(h *SubscriptionHandler, c *gin.Context) { h.BulkAction(c) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminService := newStubAdminService()
			adminService.authorizeUserMutationErr = service.ErrInsufficientPerms
			h := &SubscriptionHandler{
				subscriptionService: subscriptionService,
				adminService:        adminService,
			}
			c, recorder := privilegedMutationRequest(t, tt.method, tt.body)
			c.Params = []gin.Param{{Key: "id", Value: "99"}}

			tt.invoke(h, c)

			requirePrivilegedMutationDenied(t, recorder, adminService, 42)
		})
	}
}
