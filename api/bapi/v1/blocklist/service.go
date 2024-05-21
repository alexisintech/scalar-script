package blocklist

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// repositories
	blocklistRepo *repository.Blocklist
}

func NewService(db database.Database) *Service {
	return &Service{
		db:            db,
		blocklistRepo: repository.NewBlocklist(),
	}
}

type CreateParams struct {
	Identifier string `json:"identifier" form:"identifier" validate:"required"`
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.BlocklistIdentifierResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	identifierAttribute := userSettings.IdentifierToAttribute(
		params.Identifier,
		names.EmailAddress,
		names.PhoneNumber,
		names.Web3Wallet,
	)
	if identifierAttribute == nil {
		return nil, apierror.FormInvalidIdentifier("identifier")
	}

	var apiErr apierror.Error
	params.Identifier, apiErr = identifierAttribute.Sanitize(params.Identifier, "identifier")
	if apiErr != nil {
		return nil, apiErr
	}

	exists, err := s.blocklistRepo.ExistsByInstanceAndIdentifier(ctx, s.db, env.Instance.ID, params.Identifier)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		return nil, apierror.DuplicateBlocklistIdentifier(params.Identifier)
	}

	blocklistIdentifier := &model.BlocklistIdentifier{
		BlocklistIdentifier: &sqbmodel.BlocklistIdentifier{
			InstanceID:     env.Instance.ID,
			Identifier:     params.Identifier,
			IdentifierType: identifierAttribute.Name(),
		},
	}
	err = s.blocklistRepo.Insert(ctx, s.db, blocklistIdentifier)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.BlocklistIdentifier(blocklistIdentifier), nil
}

func (s *Service) Delete(ctx context.Context, identifierID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	exists, err := s.blocklistRepo.ExistsByIDAndInstance(ctx, s.db, identifierID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if !exists {
		return nil, apierror.BlocklistIdentifierNotFound(identifierID)
	}

	err = s.blocklistRepo.DeleteByID(ctx, s.db, identifierID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DeletedObject(identifierID, serialize.BlocklistIdentifierObjectName), nil
}

func (s *Service) ReadAll(ctx context.Context) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	identifiers, err := s.blocklistRepo.FindAllByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]interface{}, len(identifiers))
	for i, identifier := range identifiers {
		responses[i] = serialize.BlocklistIdentifier(identifier)
	}
	return serialize.Paginated(responses, int64(len(responses))), nil
}
