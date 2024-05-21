package dev_browser

import (

	// Use Go's embed package to include the html file contents in the final binary
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/cookies"
	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/segment/fapi"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

//go:embed devinit.html
var devinitHTML []byte

// DevInit provides operations related to initializing dev environment
type HTTP struct {
	cache cache.Cache
	db    database.Database

	// services
	service *Service

	// repositories
	devBrowserRepo *repository.DevBrowser
	instanceRepo   *repository.Instances
	gueClient      *gue.Client
}

// NewDevInit creates a new DevInit
func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		cache:          deps.Cache(),
		db:             deps.DB(),
		service:        NewService(deps),
		devBrowserRepo: repository.NewDevBrowser(),
		instanceRepo:   repository.NewInstances(),
		gueClient:      deps.GueClient(),
	}
}

// Init - GET /dev_browser/init
func (h *HTTP) Init(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	instance := env.Instance

	if instance.IsProduction() {
		return nil, apierror.InstanceTypeInvalid()
	}

	origin := r.URL.Query().Get("origin")
	if origin != "" {
		instance.HomeOrigin = null.StringFrom(origin)
		err := h.instanceRepo.UpdateHomeOrigin(ctx, h.db, instance)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	devBrowser := requestingdevbrowser.FromContext(ctx)
	if devBrowser == nil {
		err := renderStorageAccessPage(w)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		return nil, nil
	}

	client, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	if client != nil {
		err := sendCookie(w, client.CookieValue.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		return nil, nil
	}

	err := sendCookie(w, devBrowser.Token)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	fapi.EnqueueSegmentEvent(ctx, h.gueClient, fapi.SegmentParams{EventName: segment.APIFrontendDeveloperSessionStarted})

	return nil, nil
}

// CreateDevBrowser creates a DevBrowser for development instances.
func (h *HTTP) CreateDevBrowser(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	devBrowser := requestingdevbrowser.FromContext(ctx)
	if devBrowser != nil {
		w.WriteHeader(http.StatusOK)
		return nil, nil
	}

	env := environment.FromContext(ctx)

	if !env.Instance.IsDevelopmentOrStaging() {
		return nil, apierror.InstanceTypeInvalid()
	}

	if !env.AuthConfig.SessionSettings.URLBasedSessionSyncing {
		return nil, apierror.URLBasedSessionSyncingDisabled()
	}

	devBrowser, err := h.service.CreateDevBrowserModel(ctx, h.db, env.Instance, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = json.NewEncoder(w).Encode(devBrowser)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	fapi.EnqueueSegmentEvent(ctx, h.gueClient, fapi.SegmentParams{EventName: segment.APIFrontendDeveloperSessionStarted})

	return nil, nil
}

// SetCookie sets the initial cookie for development & staging
func (h *HTTP) SetCookie(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	if devBrowser := requestingdevbrowser.FromContext(ctx); devBrowser != nil {
		w.WriteHeader(http.StatusOK)
		return nil, nil
	}

	if env.Instance.IsProduction() {
		return nil, apierror.InstanceTypeInvalid()
	}

	newDevBrowser, err := h.service.CreateDevBrowserModel(ctx, h.db, env.Instance, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Small hack - set env.DevBrowser then call "UnsetClient" to drop a cookie
	// that holds the DevBrowser ID but no client ID
	ctx = requestingdevbrowser.NewContext(ctx, newDevBrowser)
	err = cookies.UnsetClientCookie(ctx, h.db, h.cache, w, env.Domain.AuthHost())
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	w.WriteHeader(http.StatusOK)
	return nil, nil
}

func sendCookie(w http.ResponseWriter, cookie string) error {
	w.Header().Set("Content-Type", "text/html")
	_, err := w.Write([]byte(fmt.Sprintf(`<html>
	<body>
		<script>
			window.parent.postMessage({browserToken: "%s"}, "*");
		</script>
	</body>
</html>`, cookie)))
	return err
}

func renderStorageAccessPage(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/html")
	_, err := w.Write(devinitHTML)
	return err
}
