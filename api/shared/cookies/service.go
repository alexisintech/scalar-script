package cookies

import (
	"context"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/jwt"
	clerkmaintenance "clerk/pkg/maintenance"
	"clerk/pkg/rand"
	clerksentry "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	pkiutils "clerk/utils/pki"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	cache cache.Cache
	clock clockwork.Clock
	db    database.Database

	clientDataService *client_data.Service
	devBrowserRepo    *repository.DevBrowser
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		cache:             deps.Cache(),
		clock:             deps.Clock(),
		db:                deps.DB(),
		clientDataService: client_data.NewService(deps),
		devBrowserRepo:    repository.NewDevBrowser(),
	}
}

func (s *Service) VerifyCookie(ctx context.Context, instance *model.Instance, clientCookie string) (*model.Client, apierror.Error) {
	claims, err := s.parseClientCookie(instance, clientCookie)
	if err != nil {
		return nil, err
	}

	client, err := s.verifyClient(ctx, claims)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (s *Service) parseClientCookie(instance *model.Instance, cookieVal string) (*model.ClientCookie, apierror.Error) {
	rsaPublicKey, err := pkiutils.LoadPublicKey([]byte(instance.PublicKey))
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	verifiedClaims := make(map[string]interface{})

	err = jwt.Verify(cookieVal, rsaPublicKey, &verifiedClaims, s.clock, instance.KeyAlgorithm)
	if err != nil {
		return nil, apierror.InvalidCookie(nil)
	}

	clientCookie, missingClaims := generateClientCookie(verifiedClaims, instance)

	if len(missingClaims) > 0 {
		return nil, apierror.MissingClaims(strings.Join(missingClaims, ", "))
	}

	return clientCookie, nil
}

// Try to determine client from dev browser ID or (id, rotating_token)
func (s *Service) verifyClient(ctx context.Context, clientCookie *model.ClientCookie) (*model.Client, apierror.Error) {
	// Check if we have a dev browser ID first
	if clientCookie.DevBrowserID != "" {
		var devBrowser *model.DevBrowser
		var err error
		if maintenance.FromContext(ctx) {
			var dvb model.DevBrowser
			if err := s.cache.Get(ctx, clerkmaintenance.DevBrowserKey(clientCookie.DevBrowserID, clientCookie.Instance.ID), &dvb); err != nil {
				clerksentry.CaptureException(ctx, err)
			} else if dvb.DevBrowser != nil {
				devBrowser = &dvb
			}
		}
		if devBrowser == nil {
			devBrowser, err = s.devBrowserRepo.QueryByIDAndInstance(ctx, s.db, clientCookie.DevBrowserID, clientCookie.Instance.ID)
		}
		if err != nil {
			return nil, apierror.Unexpected(err)
		} else if devBrowser == nil || !devBrowser.ClientID.Valid {
			return nil, apierror.ClientNotFoundInRequest()
		}

		client, err := s.clientDataService.QueryClient(ctx, clientCookie.Instance.ID, devBrowser.ClientID.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		} else if client == nil {
			return nil, apierror.ClientNotFound(devBrowser.ClientID.String)
		}

		return client.ToClientModel(), nil
	}

	// Check client ID & rotating token second
	if clientCookie.ClientID != "" && clientCookie.RotatingToken != "" {
		// Ensure both client id & rotating token match with an existing record
		client, err := s.clientDataService.QueryClient(ctx, clientCookie.Instance.ID, clientCookie.ClientID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		} else if client == nil {
			return nil, apierror.ClientNotFound(clientCookie.ClientID)
		}

		if client.RotatingToken != clientCookie.RotatingToken {
			return nil, apierror.InvalidRotatingToken(clientCookie.RotatingToken)
		}

		return client.ToClientModel(), nil
	}

	return nil, apierror.ClientNotFoundInRequest()
}

func generateClientCookie(claims map[string]interface{}, instance *model.Instance) (*model.ClientCookie, []string) {
	missingClaims := []string{}

	clientIDClaim := "id"
	clientID, _ := claims[clientIDClaim].(string)

	rotatingTokenClaim := "rotating_token"
	rotatingToken, _ := claims[rotatingTokenClaim].(string)

	devBrowserIDClaim := "dev"
	devBrowserID, _ := claims[devBrowserIDClaim].(string)

	if clientID == "" && rotatingToken != "" {
		missingClaims = append(missingClaims, clientIDClaim)
		return nil, missingClaims
	}

	if clientID != "" && rotatingToken == "" {
		missingClaims = append(missingClaims, rotatingTokenClaim)
		return nil, missingClaims
	}

	if clientID == "" && rotatingToken == "" && devBrowserID == "" {
		envType := constants.ToEnvironmentType(instance.EnvironmentType)

		if envType == constants.ETProduction {
			missingClaims = append(missingClaims, clientIDClaim, rotatingTokenClaim)
		} else {
			missingClaims = append(missingClaims, clientIDClaim, rotatingTokenClaim, devBrowserIDClaim)
		}

		return nil, missingClaims
	}

	return &model.ClientCookie{
		ClientID:      clientID,
		Instance:      instance,
		RotatingToken: rotatingToken,
		DevBrowserID:  devBrowserID,
	}, missingClaims
}

func (s *Service) UpdateClientCookieValue(ctx context.Context, instance *model.Instance, client *model.Client) error {
	//
	// see if there's a dev browser to add to the cookie
	// if it exists on the context, it came from the existing cookie
	// otherwise, check the database to see
	//
	var devBrowserID *string
	if instance.IsDevelopmentOrStaging() {
		devBrowser := requestingdevbrowser.FromContext(ctx)
		if devBrowser == nil {
			databaseDevBrowser, err := s.devBrowserRepo.QueryByInstanceAndClient(ctx, s.db, client.InstanceID, client.ID)
			if err != nil {
				return err
			}

			devBrowser = databaseDevBrowser
		}

		if devBrowser != nil {
			devBrowserID = &devBrowser.ID
		}
	}

	newToken, err := rand.Token()
	if err != nil {
		return err
	}

	newCookieVal, err := model.ClientCookieClass.CreateClientCookieValue(instance.PrivateKey, instance.KeyAlgorithm, client.ID, newToken, devBrowserID)
	if err != nil {
		return err
	}

	client.RotatingToken = newToken
	client.CookieValue = null.StringFrom(newCookieVal)

	updateColumns := []string{client_data.ClientColumns.RotatingToken, client_data.ClientColumns.CookieValue}
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClient(ctx, instance.ID, cdsClient, updateColumns...); err != nil {
		return err
	}
	cdsClient.CopyToClientModel(client)
	return nil
}
