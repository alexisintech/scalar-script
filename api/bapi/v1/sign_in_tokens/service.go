package sign_in_tokens

import (
	"context"
	"net/url"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ticket"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	validator *validator.Validate

	// repositories
	signInTokensRepo *repository.SignInToken
	usersRepo        *repository.Users
}

func NewService(clock clockwork.Clock, db database.Database) *Service {
	return &Service{
		clock:            clock,
		db:               db,
		validator:        validator.New(),
		signInTokensRepo: repository.NewSignInToken(),
		usersRepo:        repository.NewUsers(),
	}
}

type CreateParams struct {
	UserID           string `json:"user_id" form:"user_id" validate:"required"`
	ExpiresInSeconds *int   `json:"expires_in_seconds" form:"expires_in_seconds" validate:"omitempty,numeric,gte=1"`
}

func (p CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

// Create creates a new sign in token for the given user.
func (s *Service) Create(ctx context.Context, createParams CreateParams) (*serialize.SignInTokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if err := createParams.validate(s.validator); err != nil {
		return nil, err
	}

	userExists, err := s.usersRepo.ExistsByIDAndInstance(ctx, s.db, createParams.UserID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if !userExists {
		return nil, apierror.UserNotFound(createParams.UserID)
	}

	signInToken := &model.SignInToken{
		SignInToken: &sqbmodel.SignInToken{
			InstanceID: env.Instance.ID,
			UserID:     createParams.UserID,
			Status:     constants.StatusPending,
		},
	}

	var ticketToken string
	var ticketURL *url.URL
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.signInTokensRepo.Insert(ctx, s.db, signInToken); err != nil {
			return true, err
		}

		ticketToken, err = ticket.Generate(
			ticket.Claims{
				InstanceID:       env.Instance.ID,
				SourceType:       constants.OSTSignInToken,
				SourceID:         signInToken.ID,
				ExpiresInSeconds: createParams.ExpiresInSeconds,
			},
			env.Instance,
			s.clock,
		)
		if err != nil {
			return true, err
		}

		signInURL := env.DisplayConfig.Paths.SignInURL(
			env.Instance.Origin(env.Domain, nil),
			env.Domain.AccountsURL(),
		)
		ticketURL, err = url.Parse(signInURL)
		if err != nil {
			return true, err
		}
		q := ticketURL.Query()
		q.Add(param.ClerkTicket, ticketToken)
		ticketURL.RawQuery = q.Encode()

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.SignInToken(signInToken, ticketURL, ticketToken), nil
}

func (s *Service) Revoke(ctx context.Context, signInTokenID string) (*serialize.SignInTokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	signInToken, err := s.signInTokensRepo.QueryByIDAndInstance(ctx, s.db, signInTokenID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if signInToken == nil {
		return nil, apierror.ResourceNotFound()
	}

	if signInToken.Status != constants.StatusPending {
		return nil, apierror.SignInTokenCannotBeRevoked(signInToken.Status)
	}

	signInToken.Status = constants.StatusRevoked
	if err := s.signInTokensRepo.UpdateStatus(ctx, s.db, signInToken); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.SignInToken(signInToken, nil, ""), nil
}
