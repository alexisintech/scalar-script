package engineering

import (
	"context"
	"time"

	"clerk/api/apierror"
	"clerk/pkg/cache"
)

const (
	paramKey = "key"
)

type Service struct {
	cache cache.Cache
}

func NewService(cache cache.Cache) *Service {
	return &Service{
		cache: cache,
	}
}

type SetParams struct {
	Key   string `json:"-"`
	Value string `json:"value"`
}

func (s *Service) Set(ctx context.Context, params SetParams) apierror.Error {
	if params.Key == "" {
		return apierror.FormInvalidParameterValue(paramKey, params.Key)
	}
	err := s.cache.Set(ctx, params.Key, params.Value, 1*time.Hour)
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

type GetResponse struct {
	Value string `json:"value"`
}

func (s *Service) Get(ctx context.Context, key string) (*GetResponse, apierror.Error) {
	if key == "" {
		return nil, apierror.FormInvalidParameterValue(paramKey, key)
	}
	var value string
	err := s.cache.Get(ctx, key, &value)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return &GetResponse{Value: value}, nil
}

type ExistsResponse struct {
	Exists bool `json:"exists"`
}

func (s *Service) Exists(ctx context.Context, key string) (*ExistsResponse, apierror.Error) {
	if key == "" {
		return nil, apierror.FormInvalidParameterValue(paramKey, key)
	}

	exists, err := s.cache.Exists(ctx, key)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return &ExistsResponse{Exists: exists}, nil
}
