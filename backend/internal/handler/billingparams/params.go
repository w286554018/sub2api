package billingparams

import (
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
)

func Period(c *gin.Context) (int, int, *time.Location, error) {
	userTZ := strings.TrimSpace(c.Query("timezone"))
	now := timezone.NowInUserLocation(userTZ)
	year := now.Year()
	month := int(now.Month())

	if raw := strings.TrimSpace(c.Query("year")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, nil, infraerrors.BadRequest("BILLING_YEAR_INVALID", "year must be an integer")
		}
		year = parsed
	}
	if raw := strings.TrimSpace(c.Query("month")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, nil, infraerrors.BadRequest("BILLING_MONTH_INVALID", "month must be an integer")
		}
		month = parsed
	}
	return year, month, now.Location(), nil
}

func DateRange(c *gin.Context) (time.Time, time.Time, error) {
	userTZ := strings.TrimSpace(c.Query("timezone"))
	now := timezone.NowInUserLocation(userTZ)
	endDay := timezone.StartOfDayInUserLocation(now, userTZ)
	startDay := endDay.AddDate(0, 0, -6)

	if raw := strings.TrimSpace(c.Query("start_date")); raw != "" {
		parsed, err := timezone.ParseInUserLocation("2006-01-02", raw, userTZ)
		if err != nil {
			return time.Time{}, time.Time{}, infraerrors.BadRequest("BILLING_START_DATE_INVALID", "start_date must use YYYY-MM-DD")
		}
		startDay = parsed
	}
	if raw := strings.TrimSpace(c.Query("end_date")); raw != "" {
		parsed, err := timezone.ParseInUserLocation("2006-01-02", raw, userTZ)
		if err != nil {
			return time.Time{}, time.Time{}, infraerrors.BadRequest("BILLING_END_DATE_INVALID", "end_date must use YYYY-MM-DD")
		}
		endDay = parsed
	}
	return startDay, endDay.AddDate(0, 0, 1), nil
}
