package resource

import "clerk/model"

type FapiJWKS struct {
	fapiResource

	fapiHost string
}

func NewFapiJWKS(d *model.Domain) FapiJWKS {
	return FapiJWKS{fapiHost: d.FapiHost()}
}

func (r FapiJWKS) Route() string {
	return "/.well-known/jwks.json"
}

func (r FapiJWKS) CacheTag() string {
	return r.fapiHost + ":" + r.Route()
}
