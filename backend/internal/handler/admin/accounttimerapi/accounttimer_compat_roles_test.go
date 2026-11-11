package accounttimerapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/accounttimer"
	"github.com/gin-gonic/gin"
)

// Test access directly so all five entry points exercise their shared
// authorization boundary without depending on input validation details.
func TestCompatibilityTimerRolesIncludingDailyWindow(t *testing.T) {
	for _, role := range []string{"admin", "super_admin", "observer", "user", ""} {
		for _, actor := range []int64{0, 9} {
			for _, operation := range []string{"list", "preview", "save", "daily-window", "cancel"} {
				t.Run(role+"/"+operation+"/"+strconv.FormatInt(actor, 10), func(t *testing.T) {
					repo := &fakeRepo{}
					repositoryCalls := 0
					h := &Handler{
						Repository: func() accounttimer.Repository {
							repositoryCalls++
							return repo
						},
						Identity: func(*gin.Context) (int64, string) { return actor, role },
					}
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					c.Request = httptest.NewRequest(http.MethodPost, "/?version=1", strings.NewReader(`{"mode":"relative","hours":1,"timezone":"UTC","action":"open","open_time":"08:00","close_time":"18:00"}`))
					c.Params = gin.Params{{Key: "id", Value: "7"}, {Key: "action", Value: "open"}}
					handlers := map[string]gin.HandlerFunc{
						"list": h.List, "preview": h.Preview, "save": h.Save,
						"daily-window": h.SaveDailyWindow, "cancel": h.Cancel,
					}
					handlers[operation](c)
					allowed := actor > 0 && (role == "admin" || role == "super_admin")
					if !allowed {
						if w.Code != http.StatusForbidden || repositoryCalls != 0 || repo.calls != 0 {
							t.Fatalf("status=%d repository=%d calls=%d", w.Code, repositoryCalls, repo.calls)
						}
					} else if w.Code != http.StatusOK || repositoryCalls != 1 || repo.calls != 1 {
						t.Fatalf("administrator blocked: status=%d repository=%d calls=%d body=%s", w.Code, repositoryCalls, repo.calls, w.Body.String())
					}
				})
			}
		}
	}
}
