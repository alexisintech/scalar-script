package well_known

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// GET /.well-known/assetlinks.json
func (h *HTTP) AssetLinks(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	return h.service.AssetLinks(ctx, env.Instance)
}

// GET /.well-known/apple-app-site-association
func (h *HTTP) AppleAppSiteAssociation(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	return h.service.AppleAppSiteAssociation(ctx, env.Instance)
}

// GET /.well-known/openid-configuration
func (h *HTTP) OpenIDConfiguration(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	return h.service.OpenIDConfiguration(ctx, env.Domain)
}
