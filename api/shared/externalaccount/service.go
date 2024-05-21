package externalaccount

import (
	"context"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/oauth"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	identificationRepo *repository.Identification
	verificationRepo   *repository.Verification
}

func NewService(_ clerk.Deps) *Service {
	return &Service{
		identificationRepo: repository.NewIdentification(),
		verificationRepo:   repository.NewVerification(),
	}
}

func (s *Service) EnsureRefreshTokenExists(ctx context.Context, tx database.Tx, externalAccount *model.ExternalAccount) error {
	// No need to do anything if access token not provided (e.g. Google One Tap) OR refresh token already exists
	if !cenv.IsEnabled(cenv.FlagOAuthRefreshTokenHandlingV2) || !externalAccount.HasAccessToken() || externalAccount.HasRefreshToken() {
		return nil
	}

	oauthProvider, err := oauth.GetProvider(externalAccount.Provider)
	if err != nil {
		return err
	}

	if !oauthProvider.SupportsRefreshTokenRetrieval() {
		return nil
	}

	ident, err := s.identificationRepo.FindByID(ctx, tx, externalAccount.IdentificationID)
	if err != nil {
		return err
	}

	if !ident.VerificationID.Valid || !ident.IsVerified() {
		return nil
	}

	ver, err := s.verificationRepo.FindByID(ctx, tx, ident.VerificationID.String)
	if err != nil {
		return err
	}

	if err := ver.SetCustomError(apierror.ExternalAccountMissingRefreshToken()); err != nil {
		return err
	}

	return s.verificationRepo.UpdateError(ctx, tx, ver)
}
