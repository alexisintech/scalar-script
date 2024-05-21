package clients

import (
	"context"
	"net/http"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"

	"github.com/clerk/clerk-sdk-go/v2/jwks"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, jwksClient *jwks.Client) *HTTP {
	return &HTTP{
		service: NewService(deps, jwksClient),
	}
}

// Middleware /integrations
func (h *HTTP) RequireClient(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	var client *model.Client
	var err apierror.Error

	// Try to get JWT from header if exists
	if jwtHeader := r.Header.Get("Authorization"); jwtHeader != "" {
		// Header exists, verify token and get client
		client, err = h.service.VerifyClientFromJWT(ctx, jwtHeader)
		if err != nil {
			return nil, err
		}
	} else {
		// Get client ID from query parameter
		clientID := r.URL.Query().Get("client_id")
		client, err = h.service.GetClient(ctx, clientID)
		if err != nil {
			return nil, err
		}
	}

	ctx = context.WithValue(ctx, ctxkeys.RequestingClient, client)
	return r.WithContext(ctx), nil
}
