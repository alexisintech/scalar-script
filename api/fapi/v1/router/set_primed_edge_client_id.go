package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/primed_edge_client_id"
	"clerk/pkg/jwt"
	pkiutils "clerk/utils/pki"

	"github.com/jonboulle/clockwork"
)

type DurableObjectReference struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

func setPrimedEdgeClientID(clock clockwork.Clock) clerkhttp.MiddlewareFunc {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		primedClientJWT := r.Header.Get(constants.XEdgePrimedClient)
		if primedClientJWT == "" {
			return r.WithContext(ctx), nil
		}

		instance := environment.FromContext(ctx).Instance
		durableObjectReference := DurableObjectReference{}
		rsaPublicKey, err := pkiutils.LoadPublicKey([]byte(instance.PublicKey))
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		err = jwt.Verify(primedClientJWT, rsaPublicKey, &durableObjectReference, clock, instance.KeyAlgorithm)
		if err != nil {
			return nil, apierror.BadRequest()
		}

		newCtx := primed_edge_client_id.NewContext(ctx, durableObjectReference.ID)
		return r.WithContext(newCtx), nil
	}
}
