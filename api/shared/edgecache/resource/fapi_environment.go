package resource

import "clerk/model"

type FapiEnvironment struct {
	fapiResource

	fapiHost string
}

func NewFapiEnvironment(d *model.Domain) FapiEnvironment {
	return FapiEnvironment{fapiHost: d.FapiHost()}
}

func (r FapiEnvironment) Route() string {
	return "/v1/environment"
}

func (r FapiEnvironment) CacheTag() string {
	return r.cacheTag(r.fapiHost, r.Route())
}
