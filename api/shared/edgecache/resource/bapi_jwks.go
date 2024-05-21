package resource

type BapiJWKS struct {
	bapiResource

	hashedSecretKey hashedInstanceSecretKey
}

func NewBapiJWKS(instanceSecretKey string) (*BapiJWKS, error) {
	r := BapiJWKS{}

	var err error
	r.hashedSecretKey, err = r.hash(instanceSecretKey)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

func (r *BapiJWKS) Route() string {
	return "/v1/jwks"
}

func (r *BapiJWKS) CacheTag() string {
	return r.cacheTag(r.hashedSecretKey, r.Route())
}
