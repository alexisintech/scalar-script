package analytics

import (
	"net/http"
	"time"

	"clerk/api/apierror"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	clock   clockwork.Clock
	service *Service
}

func NewHTTP(clock clockwork.Clock, db database.Database) *HTTP {
	return &HTTP{
		clock:   clock,
		service: NewService(clock, db),
	}
}

const isoDateFmt = "2006-01-02"

// GET /instances/{instanceID}/analytics/user_activity/{kind}
func (h *HTTP) UserActivity(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	since, err := time.Parse(isoDateFmt, r.FormValue("since"))
	if err != nil {
		since = time.Time{}
	}

	until, err := time.Parse(isoDateFmt, r.FormValue("until"))
	if err != nil {
		until = h.clock.Now().UTC()
	}

	interval := "day"
	formInterval := r.FormValue("interval")
	switch formInterval {
	case "week", "month", "quarter", "year":
		interval = formInterval
	}

	kind := chi.URLParam(r, "kind")
	switch kind {
	case "active_users":
		return h.service.ActiveUsers(r.Context(), instanceID, since, until, interval)
	case "signups":
		return h.service.Signups(r.Context(), instanceID, since, until, interval)
	case "signins":
		return h.service.Signins(r.Context(), instanceID, since, until, interval)
	default:
		return nil, apierror.URLNotFound()
	}
}

// GET /instances/{instanceID}/analytics/monthly_metrics
func (h *HTTP) MonthlyMetrics(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	now := h.clock.Now().UTC() // we need the data of the current month
	year := now.Year()
	month := now.Month()
	return h.service.MonthlyMetrics(r.Context(), instanceID, year, month)
}

// GET /instances/{instanceID}/analytics/latest_activity
func (h *HTTP) LatestActivity(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.LatestActivity(r.Context(), instanceID, 10)
}
