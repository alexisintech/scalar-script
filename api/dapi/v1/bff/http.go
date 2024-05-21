package bff

import (
	"net/http"
	"unicode/utf8"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, clock clockwork.Clock, newSDKConfig sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, clock, newSDKConfig),
	}
}

// GET /instances/{instanceID}/bff/api_keys
func (h *HTTP) APIKeys(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.APIKeys(r.Context())
}

// GET /instances/{instanceID}/bff/users
func (h *HTTP) Users(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := ListParams{
		orderBy:         r.FormValue(param.OrderBy.Name),
		query:           r.FormValue(param.Query.Name),
		organizationIDs: form.GetStringArray(r.Form, "organization_id"),
	}

	if params.query != "" && utf8.RuneCountInString(params.query) < 3 {
		return nil, apierror.FormParameterMinLengthExceeded("search", 3)
	}

	pagination, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	instanceID := chi.URLParam(r, "instanceID")

	return h.service.ListUsersWithSettings(r.Context(), instanceID, params, pagination)
}
