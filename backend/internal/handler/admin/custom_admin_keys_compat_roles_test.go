//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminKeyJWTCompatibilityRoles(t *testing.T) {
	for _, role := range []string{"admin", "super_admin", "observer", "user", ""} {
		for _, method := range []string{service.AuditAuthMethodJWT, service.AuditAuthMethodAdminAPIKey, service.AuditAuthMethodMCPOAuth, ""} {
			t.Run(role+"/"+method, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
				c.Set("auth_method", method)
				c.Set("user_role", role)
				// Full access remains a machine principal, including when
				// its creator is a super administrator.
				if method == service.AuditAuthMethodAdminAPIKey {
					c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{
						KeyID: "full-access-key", CreatorID: 9,
						Policy: adminaccess.Policy{FullAccess: true},
					}))
				}
				want := method == service.AuditAuthMethodJWT && (role == "admin" || role == "super_admin")
				require.Equal(t, want, requireAdminKeyJWT(c))
				if !want {
					require.Equal(t, http.StatusForbidden, w.Code)
				}
			})
		}
	}
}

func TestAdminKeyHandlersRejectFullAccessMachineAndObserver(t *testing.T) {
	h := &SettingHandler{}
	for _, handler := range []gin.HandlerFunc{
		h.ListScopedAdminKeys, h.CreateScopedAdminKey, h.UpdateScopedAdminKey,
		h.RotateScopedAdminKey, h.RevokeScopedAdminKey, h.DeleteScopedAdminKeyRecord,
		h.GetAdminAPIKey, h.RegenerateAdminAPIKey, h.DeleteAdminAPIKey,
	} {
		for _, identity := range []struct{ method, role string }{
			{service.AuditAuthMethodAdminAPIKey, "admin"},
			{service.AuditAuthMethodAdminAPIKey, "super_admin"},
			{service.AuditAuthMethodJWT, "observer"},
			{service.AuditAuthMethodJWT, "user"},
		} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
			c.Set("auth_method", identity.method)
			c.Set("user_role", identity.role)
			if identity.method == service.AuditAuthMethodAdminAPIKey {
				c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{
					KeyID: "full-access-key", Policy: adminaccess.Policy{FullAccess: true},
				}))
			}
			require.NotPanics(t, func() { handler(c) })
			require.Equal(t, http.StatusForbidden, w.Code, identity)
		}
	}
}
