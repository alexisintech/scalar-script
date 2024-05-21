package users

import (
	"context"
	"net/url"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/images"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/storage/google"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
)

type ProxyImageURLService struct {
	db                   database.Database
	userRepo             *repository.Users
	imagesRepo           *repository.Images
	externalAccountsRepo *repository.ExternalAccount
	validator            *validator.Validate
}

func NewProxyImageURLService(db database.Database) *ProxyImageURLService {
	return &ProxyImageURLService{
		db:                   db,
		userRepo:             repository.NewUsers(),
		imagesRepo:           repository.NewImages(),
		externalAccountsRepo: repository.NewExternalAccount(),
		validator:            validator.New(),
	}
}

func (s *ProxyImageURLService) ProxyImageURL(ctx context.Context, params ProxyImageURLParams) (*serialize.ProxyImageURLResponse, apierror.Error) {
	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	// it's safe to ignore the error since it's being validate against url_encoded
	imageURL, _ := url.QueryUnescape(params.ImageURL)

	imageID, extractErr := images.ExtractImageIDFromImageURL(imageURL)
	if extractErr != nil {
		sentryclerk.CaptureException(ctx, extractErr)
		return nil, apierror.ImageNotFound()
	}

	// If image record exists (image is uploaded), return the PublicURL
	image, err := s.imagesRepo.QueryByID(ctx, s.db, imageID)
	if image != nil {
		if err != nil {
			sentryclerk.CaptureException(ctx, err)
		}
		return serialize.ProxyImageURL(image.PublicURL), nil
	}

	// Find user using imageURL as profile_image_public_url
	// If not users.profile_image_public_url return http 404
	user, err := s.userRepo.QueryByProfileImagePublicURL(ctx, s.db, google.GcsURL(imageURL))
	if user == nil {
		if err != nil {
			sentryclerk.CaptureException(ctx, err)
		}
		return nil, apierror.ImageNotFound()
	}

	provider, err := images.ExtractPrefixFromImageURL(imageURL)
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
		return nil, apierror.ImageNotFound()
	}

	// Find external_accounts.avatar_url from user first identification
	externalAccount, err := s.externalAccountsRepo.QueryVerifiedByUserIDAndProviderAndInstance(ctx, s.db, user.ID, provider, user.InstanceID)
	if externalAccount == nil {
		if err != nil {
			sentryclerk.CaptureException(ctx, err)
		}
		return nil, apierror.ImageNotFound()
	}

	return serialize.ProxyImageURL(externalAccount.AvatarURL), nil
}

type ProxyImageURLParams struct {
	ImageURL string `json:"image_url" validate:"required,url|url_encoded"`
}

func (p ProxyImageURLParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}
