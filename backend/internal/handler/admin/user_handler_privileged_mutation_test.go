//go:build unit

package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func privilegedMutationRequest(t *testing.T, method, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = []gin.Param{{Key: "id", Value: "42"}}
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
	return c, recorder
}

func requirePrivilegedMutationDenied(t *testing.T, recorder *httptest.ResponseRecorder, svc *stubAdminService, targetUserIDs ...int64) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	expected := append([]int64{7}, targetUserIDs...)
	require.Equal(t, [][]int64{expected}, svc.authorizeUserMutationCalls)
}

func TestUserMutationHandlers_BlockPlainAdminFromPrivilegedTargets(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		invoke func(*UserHandler, *gin.Context)
	}{
		{
			name:   "bind auth identity",
			method: http.MethodPost,
			body:   `{"provider_type":"oidc","provider_key":"google","provider_subject":"subject-1"}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.BindAuthIdentity(c) },
		},
		{
			name:   "update balance",
			method: http.MethodPost,
			body:   `{"balance":1,"operation":"add"}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.UpdateBalance(c) },
		},
		{
			name:   "replace group",
			method: http.MethodPost,
			body:   `{"old_group_id":1,"new_group_id":2}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.ReplaceGroup(c) },
		},
		{
			name:   "update platform quotas",
			method: http.MethodPut,
			body:   `{"quotas":[{"platform":"openai","daily_limit_usd":10}]}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.UpdateUserPlatformQuotas(c) },
		},
		{
			name:   "reset platform quota",
			method: http.MethodPost,
			body:   `{"platform":"openai","window":"daily"}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.ResetUserPlatformQuotaWindow(c) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newStubAdminService()
			svc.authorizeUserMutationErr = service.ErrInsufficientPerms
			h := &UserHandler{
				adminService:          svc,
				userPlatformQuotaRepo: &upsertCapturingQuotaRepo{},
			}
			c, recorder := privilegedMutationRequest(t, tt.method, tt.body)

			tt.invoke(h, c)

			requirePrivilegedMutationDenied(t, recorder, svc, 42)
		})
	}
}

func TestBatchUserMutationHandlers_AuthorizeEverySelectedTarget(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		invoke func(*UserHandler, *gin.Context)
	}{
		{
			name:   "concurrency",
			body:   `{"user_ids":[41,42],"concurrency":3,"mode":"set"}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.BatchUpdateConcurrency(c) },
		},
		{
			name:   "limits",
			body:   `{"user_ids":[41,42],"rpm_limit":5}`,
			invoke: func(h *UserHandler, c *gin.Context) { h.BatchUpdateLimits(c) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newStubAdminService()
			svc.authorizeUserMutationErr = service.ErrInsufficientPerms
			h := &UserHandler{adminService: svc}
			c, recorder := privilegedMutationRequest(t, http.MethodPost, tt.body)

			tt.invoke(h, c)

			requirePrivilegedMutationDenied(t, recorder, svc, 41, 42)
		})
	}
}

func TestBatchUserMutationAll_AuthorizesExpandedUserSet(t *testing.T) {
	svc := newStubAdminService()
	svc.users = append(svc.users, service.User{ID: 2, Role: service.RoleAdmin, Status: service.StatusActive})
	svc.authorizeUserMutationErr = service.ErrInsufficientPerms
	h := &UserHandler{adminService: svc}
	c, recorder := privilegedMutationRequest(t, http.MethodPost, `{"all":true,"concurrency":3,"mode":"set"}`)

	h.BatchUpdateConcurrency(c)

	requirePrivilegedMutationDenied(t, recorder, svc, 1, 2)
}

func TestUpdateUserAttributes_BlocksPlainAdminFromPrivilegedTarget(t *testing.T) {
	svc := newStubAdminService()
	svc.authorizeUserMutationErr = service.ErrInsufficientPerms
	h := &UserAttributeHandler{adminService: svc}
	c, recorder := privilegedMutationRequest(t, http.MethodPut, `{"values":{"1":"protected"}}`)

	h.UpdateUserAttributes(c)

	requirePrivilegedMutationDenied(t, recorder, svc, 42)
}
