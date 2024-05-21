package passkeys

import (
	"clerk/api/shared/comms"
	"clerk/api/shared/user_profile"
	"clerk/model"
	clerkwebauthn "clerk/pkg/webauthn"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"context"
	"fmt"
	"strings"

	"github.com/segmentio/ksuid"
)

type Service struct {
	identificationRepo *repository.Identification
	passkeyRepo        *repository.Passkey
	commsService       *comms.Service
	userProfileService *user_profile.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		identificationRepo: repository.NewIdentification(),
		passkeyRepo:        repository.NewPasskey(),
		commsService:       comms.NewService(deps),
		userProfileService: user_profile.NewService(deps.Clock()),
	}
}

func (s *Service) GetWebAuthnUser(ctx context.Context, tx database.Tx, instanceID string, user *model.User) (*clerkwebauthn.User, error) {
	// webAuthn ID
	// use []byte that backs the ksuid portion of clerk user ID
	userKsuid, err := ksuid.Parse(strings.Split(user.ID, "_")[1])
	if err != nil {
		return nil, err
	}
	webAuthnID := userKsuid.Bytes()

	webAuthnName, err := clerkwebauthn.GetWebAuthnName(ctx, tx, s.identificationRepo, user)
	if err != nil {
		return nil, err
	}

	webAuthnDisplayName := clerkwebauthn.GetWebAuthnDisplayName(user, webAuthnName)

	credentials, err := clerkwebauthn.GetExistingPasskeys(ctx, tx, instanceID, s.passkeyRepo, user)
	if err != nil {
		return nil, err
	}

	return clerkwebauthn.NewUser(
		credentials,
		webAuthnID,
		webAuthnName,
		webAuthnDisplayName,
	), nil
}

func (s *Service) SendPasskeyNotification(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	user *model.User,
	passkeyName string,
	templateSlug string,
) error {
	primaryEmailAddress, err := s.userProfileService.GetPrimaryEmailAddress(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryEmailAddress != nil {
		return s.commsService.SendPasskeyEmail(ctx, tx, env, comms.EmailPasskey{
			PasskeyName: passkeyName,
			GreetingName: strings.TrimSpace(fmt.Sprintf("%s %s",
				user.FirstName.String, user.LastName.String)),
			PrimaryEmailAddress: *primaryEmailAddress,
		}, templateSlug)
	}

	primaryPhoneNumber, err := s.userProfileService.GetPrimaryPhoneNumber(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryPhoneNumber != nil {
		return s.commsService.SendPasskeySMS(ctx, tx, env, *primaryPhoneNumber, templateSlug)
	}

	return nil
}

func (s *Service) GetRpIDOriginForProductionInstances(ctx context.Context, tx database.Tx, env *model.Env) (string, error) {
	if env.Domain.IsPrimary(env.Instance) {
		return "https://" + env.Domain.Name, nil
	}

	domain, err := repository.NewDomain().FindByID(ctx, tx, env.Instance.ActiveDomainID)
	if err != nil {
		return "", err
	}
	return "https://" + domain.Name, nil
}
