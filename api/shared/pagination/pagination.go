package pagination

import (
	"net/http"
	"strconv"

	"clerk/api/apierror"
	"clerk/utils/param"

	"github.com/go-playground/validator/v10"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
)

const (
	MinLimit     = 1
	MaxLimit     = 500
	DefaultLimit = 10
)

type Params struct {
	Limit  int `validate:"gte=1,lte=500"`
	Offset int `validate:"gte=0"`
}

func NewFromRequest(r *http.Request) (Params, apierror.Error) {
	var params Params
	var err error

	limit := r.URL.Query().Get(param.Limit.Name)

	if limit != "" {
		params.Limit, err = strconv.Atoi(limit)
		if err != nil {
			return params, apierror.FormInvalidParameterValue("limit", limit)
		}

		// Convert invalid values to default
		if params.Limit < MinLimit || params.Limit > MaxLimit {
			params.Limit = DefaultLimit
		}
	} else {
		params.Limit = DefaultLimit
	}

	offset := r.URL.Query().Get(param.Offset.Name)

	if offset != "" {
		params.Offset, err = strconv.Atoi(offset)
		if err != nil {
			return params, apierror.FormInvalidParameterValue("offset", offset)
		}

		// Convert negative values to 0
		if params.Offset < 0 {
			params.Offset = 0
		}
	}

	if err = validator.New().Struct(params); err != nil {
		return params, apierror.FormValidationFailed(err)
	}

	return params, nil
}

func (p Params) ToQueryMods() []qm.QueryMod {
	queryMods := []qm.QueryMod{}

	if p.Limit != 0 {
		queryMods = append(queryMods, qm.Limit(p.Limit))
	}

	if p.Offset != 0 {
		queryMods = append(queryMods, qm.Offset(p.Offset))
	}

	return queryMods
}
