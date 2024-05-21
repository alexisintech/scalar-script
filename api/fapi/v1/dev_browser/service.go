package dev_browser

import (
	"context"

	// Use Go's embed package to include the html file contents in the final binary
	_ "embed"
	"time"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/jwt"
	clerkmaintenance "clerk/pkg/maintenance"
	"clerk/pkg/rand"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	clerkurl "clerk/utils/url"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	cache          cache.Cache
	db             database.Database
	devBrowserRepo *repository.DevBrowser
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		cache:          deps.Cache(),
		db:             deps.DB(),
		devBrowserRepo: repository.NewDevBrowser(),
	}
}

func (s *Service) CreateDevBrowserModel(ctx context.Context, exec database.Executor, instance *model.Instance, client *model.Client) (*model.DevBrowser, error) {
	devBrowserID := rand.InternalClerkID(constants.IDPDevBrowser)

	// Hack: Now this is being used as the __client cookie when there is no client.
	claims := map[string]interface{}{"dev": devBrowserID}
	browserToken, err := jwt.GenerateToken(instance.PrivateKey, claims, instance.KeyAlgorithm)
	if err != nil {
		return nil, err
	}

	devBrowser := &model.DevBrowser{DevBrowser: &sqbmodel.DevBrowser{
		ID:         devBrowserID,
		InstanceID: instance.ID,
		Token:      browserToken,
	}}

	if client != nil {
		devBrowser.ClientID = null.StringFrom(client.ID)
	}

	if maintenance.FromContext(ctx) {
		// we need to create the dev browser in Redis
		err = s.cache.Set(ctx, clerkmaintenance.DevBrowserKey(devBrowserID, instance.ID), devBrowser, time.Hour)
	} else {
		err = s.devBrowserRepo.Insert(ctx, exec, devBrowser)
	}
	if err != nil {
		return nil, err
	}

	return devBrowser, nil
}

func (s *Service) UpdateHomeOrigin(ctx context.Context, devBrowser *model.DevBrowser, url string) error {
	// Skip if there is no devBrowser
	if devBrowser == nil {
		return nil
	}

	origin, err := clerkurl.ExtractOrigin(url)
	if err != nil {
		return err
	}

	originType, err := clerkurl.GetOriginType(origin)
	if err != nil {
		return err
	}

	// Skip for AP
	if originType == clerkurl.Accounts {
		return nil
	}

	devBrowser.HomeOrigin = null.StringFrom(origin)
	return s.devBrowserRepo.UpdateHomeOrigin(ctx, s.db, devBrowser)
}
