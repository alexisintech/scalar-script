package strategies

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/samlaccount"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/jwt"
	"clerk/pkg/ticket"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type TicketAttemptor struct {
	cache  cache.Cache
	clock  clockwork.Clock
	ticket string
	env    *model.Env
	signIn *model.SignIn
	signUp *model.SignUp

	// services
	samlAccountService *samlaccount.Service

	// repositories
	identificationRepo     *repository.Identification
	actorTokenRepo         *repository.ActorToken
	instanceInvitationRepo *repository.Invitations
	orgRepo                *repository.Organization
	orgInvitationRepo      *repository.OrganizationInvitation
	samlConnectionRepo     *repository.SAMLConnection
	signInRepo             *repository.SignIn
	signInTokenRepo        *repository.SignInToken
	signUpRepo             *repository.SignUp
	userRepo               *repository.Users
	verificationRepo       *repository.Verification
}

type TicketAttemptorParams struct {
	Ticket string
	SignIn *model.SignIn
	SignUp *model.SignUp
}

func NewTicketAttemptor(deps clerk.Deps, env *model.Env, params TicketAttemptorParams) TicketAttemptor {
	return TicketAttemptor{
		cache:                  deps.Cache(),
		clock:                  deps.Clock(),
		ticket:                 params.Ticket,
		env:                    env,
		signIn:                 params.SignIn,
		signUp:                 params.SignUp,
		samlAccountService:     samlaccount.NewService(deps),
		identificationRepo:     repository.NewIdentification(),
		actorTokenRepo:         repository.NewActorToken(),
		instanceInvitationRepo: repository.NewInvitations(),
		orgInvitationRepo:      repository.NewOrganizationInvitation(),
		orgRepo:                repository.NewOrganization(),
		samlConnectionRepo:     repository.NewSAMLConnection(),
		signInRepo:             repository.NewSignIn(),
		signInTokenRepo:        repository.NewSignInToken(),
		signUpRepo:             repository.NewSignUp(),
		userRepo:               repository.NewUsers(),
		verificationRepo:       repository.NewVerification(),
	}
}

func (a TicketAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	claims, err := ticket.Parse(a.ticket, a.env.Instance, a.clock)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("ticket/attempt: token %s has expired: %w",
				a.ticket, ErrTicketExpired)
		}
		return nil, fmt.Errorf("ticket/attempt: parsing ticket %s: %w",
			a.ticket, ErrTicketInvalid)
	}

	switch claims.SourceType {
	case constants.OSTActorToken:
		return a.handleActorToken(ctx, tx, claims)
	case constants.OSTInvitation:
		return a.handleInstanceInvitation(ctx, tx, claims)
	case constants.OSTOrganizationInvitation:
		return a.handleOrganizationInvitation(ctx, tx, claims)
	case constants.OSTSignInToken:
		return a.handleSignInToken(ctx, tx, claims)
	case constants.OSTSAMLIdpInitiated:
		return a.handleSAMLIdPInitiated(ctx, tx, claims)
	default:
		return nil, fmt.Errorf("ticket/attempt: invalid source type %s: %w",
			claims.SourceType, ErrTicketInvalid)
	}
}

func (a TicketAttemptor) handleInstanceInvitation(
	ctx context.Context,
	tx database.Tx,
	claims ticket.Claims,
) (*model.Verification, error) {
	invitation, err := a.instanceInvitationRepo.QueryByID(ctx, tx, claims.SourceID)
	if err != nil {
		return nil, fmt.Errorf("ticket/attempt: retrieving instance invitation %s: %w",
			claims.SourceID, err)
	} else if invitation == nil {
		return nil, fmt.Errorf("ticket/attempt: instance invitation %s  in instance %s for ticket %s was not found",
			claims.SourceID, claims.InstanceID, a.ticket)
	}

	if invitation.IsAccepted() {
		return nil, fmt.Errorf("ticket/attempt: instance invitation %s already accepted, sign in instead: %w",
			claims.SourceID, ErrInstanceInvitationAlreadyAccepted)
	}
	if invitation.IsRevoked() {
		return nil, fmt.Errorf("ticket/attempt: instance invitation %s was revoked: %w",
			claims.SourceID, ErrInstanceInvitationRevoked)
	}

	verification := &model.Verification{
		Verification: &sqbmodel.Verification{
			InstanceID: claims.InstanceID,
			Strategy:   constants.VSTicket,
			Attempts:   1,
			Token:      null.StringFrom(a.ticket),
		},
	}

	if err := a.verificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: creating new verification %+v for %s: %w",
			verification, a.ticket, err)
	}

	identification, err := a.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, tx, claims.InstanceID, invitation.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, fmt.Errorf("ticket/attempt: checking if (%s, %s, %s) is unique: %w",
			invitation.EmailAddress, constants.ITEmailAddress, claims.InstanceID, err)
	}

	if a.signIn != nil {
		if identification == nil {
			return nil, fmt.Errorf("ticket/attempt: identifier %s of token %s doesn't exist in %s: %w",
				invitation.EmailAddress, a.ticket, invitation.InstanceID, ErrInstanceInvitationIdentificationNotFound)
		}
	} else if a.signUp != nil {
		if identification != nil {
			return nil, fmt.Errorf("ticket/attempt: email address %s is already taken, try another one: %w",
				invitation.EmailAddress, ErrInstanceInvitationIdentificationAlreadyExists)
		}

		identification = &model.Identification{
			Identification: &sqbmodel.Identification{
				InstanceID:     claims.InstanceID,
				Type:           constants.ITEmailAddress,
				VerificationID: null.StringFrom(verification.ID),
				Identifier:     null.StringFrom(invitation.EmailAddress),
				Status:         constants.ISVerified,
			},
		}

		identification.SetCanonicalIdentifier()
		if err := a.identificationRepo.Insert(ctx, tx, identification); err != nil {
			return nil, fmt.Errorf("ticket/attempt: creating new identification %+v for %s: %w",
				identification, a.ticket, err)
		}
	}

	verification.IdentificationID = null.StringFrom(identification.ID)
	if err := a.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: updating identification id to %s on verification %s: %w",
			identification.ID, verification.ID, err)
	}

	invitation.IdentificationID = null.StringFrom(identification.ID)
	if err := a.instanceInvitationRepo.UpdateIdentificationID(ctx, tx, invitation); err != nil {
		return nil, fmt.Errorf("ticket/attempt: updating instance invitation's %+v identification id with %s: %w",
			invitation, identification.ID, err)
	}

	if a.signIn != nil {
		a.signIn.IdentificationID = null.StringFrom(identification.ID)
		a.signIn.InvitationID = null.StringFrom(invitation.ID)
		if err := a.signInRepo.Update(ctx, tx, a.signIn, sqbmodel.SignInColumns.IdentificationID, sqbmodel.SignInColumns.InvitationID); err != nil {
			return nil, fmt.Errorf("ticket/attempt: updating identification and organization invitation id on sign in %+v: %w",
				a.signIn, err)
		}
	} else if a.signUp != nil {
		a.signUp.EmailAddressID = null.StringFrom(identification.ID)
		a.signUp.InstanceInvitationID = null.StringFrom(invitation.ID)
		a.signUp.PublicMetadata = invitation.PublicMetadata
		if err := a.signUpRepo.Update(ctx, tx, a.signUp, sqbmodel.SignUpColumns.EmailAddressID, sqbmodel.SignUpColumns.InstanceInvitationID, sqbmodel.SignUpColumns.PublicMetadata); err != nil {
			return nil, fmt.Errorf("invitation/attempt: updating email address and public metadata of sign up %+v: %w",
				a.signUp, err)
		}
	}

	return verification, nil
}

func (a TicketAttemptor) handleOrganizationInvitation(
	ctx context.Context,
	tx database.Tx,
	claims ticket.Claims) (*model.Verification, error) {
	if claims.OrganizationID != nil {
		org, err := a.orgRepo.QueryByID(ctx, tx, *claims.OrganizationID)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: retrieving organization %s: %w",
				*claims.OrganizationID, err)
		}
		if org == nil {
			return nil, fmt.Errorf("ticket/attempt: organization %s for ticket %s was not found: %w",
				*claims.OrganizationID, a.ticket, ErrOrganizationInvitationToDeletedOrganization)
		}
	}

	invitation, err := a.orgInvitationRepo.QueryByID(ctx, tx, claims.SourceID)
	if err != nil {
		return nil, err
	} else if invitation == nil {
		return nil, fmt.Errorf("ticket/attempt: organization invitation %s in instance %s for token %s was not found: %w",
			claims.SourceID, claims.InstanceID, a.ticket, ErrOrganizationInvitationNotFound)
	}

	if invitation.IsRevoked() {
		return nil, fmt.Errorf("ticket/attempt: invitation %s was revoked: %w",
			invitation.ID, ErrOrganizationInvitationRevoked)
	}

	if invitation.IsAccepted() {
		return nil, fmt.Errorf("ticket/attempt: invitation %s has already been accepted: %w",
			invitation.ID, ErrOrganizationInvitationAlreadyAccepted)
	}

	identification, err := a.identificationRepo.QueryClaimedVerifiedByInstanceAndIdentifierAndType(ctx, tx, invitation.InstanceID, invitation.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, fmt.Errorf("ticket/attempt: retrieving identification for %s in instance %s: %w",
			invitation.EmailAddress, invitation.InstanceID, err)
	}

	if a.signIn != nil {
		// If in the context of a sign in, check if the identifier already exists. If it doesn't, return an error
		if identification == nil {
			return nil, fmt.Errorf("ticket/attempt: identifier %s of token %s doesn't exist in %s: %w",
				invitation.EmailAddress, a.ticket, invitation.InstanceID, ErrOrganizationInvitationIdentificationNotFound)
		}
	} else if a.signUp != nil {
		// If in the context of a sign up, check if the identifier doesn't exist. If it exists, return an error.
		if identification != nil {
			return nil, fmt.Errorf("ticket/attempt: identifier %s of token %s already exists in %s: %w",
				invitation.EmailAddress, a.ticket, invitation.InstanceID, ErrOrganizationInvitationIdentificationAlreadyExists)
		}

		// Create the new identifier.
		identification = &model.Identification{Identification: &sqbmodel.Identification{
			InstanceID: invitation.InstanceID,
			Type:       constants.ITEmailAddress,
			Identifier: null.StringFrom(invitation.EmailAddress),
			Status:     constants.ISNotSet,
		}}
		identification.SetCanonicalIdentifier()
		if err := a.identificationRepo.Insert(ctx, tx, identification); err != nil {
			return nil, err
		}
	}

	verification := &model.Verification{
		Verification: &sqbmodel.Verification{
			InstanceID: claims.InstanceID,
			Strategy:   constants.VSTicket,
			Attempts:   1,
			Token:      null.StringFrom(a.ticket),
		},
	}

	if err := a.verificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: creating new verification %+v for %s: %w",
			verification, a.ticket, err)
	}

	verification.IdentificationID = null.StringFrom(identification.ID)
	if err := a.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: updating identification id to %s on verification %s: %w",
			identification.ID, verification.ID, err)
	}

	if a.signIn != nil {
		// Update given sign in with the identification and organization invitation id
		a.signIn.IdentificationID = null.StringFrom(identification.ID)
		a.signIn.OrganizationInvitationID = null.StringFrom(invitation.ID)
		if err := a.signInRepo.Update(ctx, tx, a.signIn, sqbmodel.SignInColumns.IdentificationID, sqbmodel.SignInColumns.OrganizationInvitationID); err != nil {
			return nil, fmt.Errorf("ticket/attempt: updating identification and organization invitation id on sign in %+v: %w",
				a.signIn, err)
		}
	} else if a.signUp != nil {
		// Update sign up with the identification and organization invitation id
		a.signUp.EmailAddressID = null.StringFrom(identification.ID)
		a.signUp.OrganizationInvitationID = null.StringFrom(invitation.ID)
		if err := a.signUpRepo.Update(ctx, tx, a.signUp, sqbmodel.SignUpColumns.EmailAddressID, sqbmodel.SignUpColumns.OrganizationInvitationID); err != nil {
			return nil, fmt.Errorf("ticket/attempt: updating identification and organization invitation id on sign up %+v: %w",
				a.signUp, err)
		}

		identification.VerificationID = null.StringFrom(verification.ID)
		identification.Status = constants.ISVerified
		err := a.identificationRepo.Update(ctx, tx, identification,
			sqbmodel.IdentificationColumns.VerificationID,
			sqbmodel.IdentificationColumns.Status)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: updating verification and verified flag in newly created identification %+v: %w",
				identification, err)
		}
	}

	return verification, nil
}

func (a TicketAttemptor) handleSignInToken(
	ctx context.Context,
	tx database.Tx,
	claims ticket.Claims) (*model.Verification, error) {
	signInToken, err := a.signInTokenRepo.QueryByID(ctx, tx, claims.SourceID)
	if err != nil {
		return nil, err
	}

	if signInToken == nil {
		return nil, ErrSignInTokenNotFound
	} else if signInToken.Status == constants.StatusRevoked {
		return nil, ErrSignInTokenRevoked
	} else if signInToken.Status == constants.StatusAccepted {
		return nil, ErrSignInTokenAlreadyAccepted
	}

	if a.signIn == nil {
		return nil, ErrSignInTokenUsedOutsideOfSignIn
	}

	user, err := a.userRepo.FindByID(ctx, tx, signInToken.UserID)
	if err != nil {
		return nil, err
	}

	// We now need to find an identification for this user, so that
	// we can use it in the sign in object.
	// Remember that sign ins are bound to a particular identification
	// and not to a user.
	identificationID, err := a.findIdentificationForUser(ctx, tx, user, claims.InstanceID)
	if err != nil {
		return nil, err
	}

	// Create verification
	verification := &model.Verification{
		Verification: &sqbmodel.Verification{
			InstanceID: claims.InstanceID,
			Strategy:   constants.VSTicket,
			Attempts:   1,
			Token:      null.StringFrom(a.ticket),
		},
	}

	if err := a.verificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: creating new verification %+v for %s: %w",
			verification, a.ticket, err)
	}

	verification.IdentificationID = null.StringFrom(identificationID)
	if err := a.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: updating identification id to %s on verification %s: %w",
			identificationID, verification.ID, err)
	}

	a.signIn.IdentificationID = null.StringFrom(identificationID)
	a.signIn.FirstFactorCurrentVerificationID = null.StringFromPtr(nil)
	a.signIn.FirstFactorSuccessVerificationID = null.StringFrom(verification.ID)

	err = a.signInRepo.Update(
		ctx, tx, a.signIn,
		sqbmodel.SignInColumns.IdentificationID,
		sqbmodel.SignInColumns.FirstFactorCurrentVerificationID,
		sqbmodel.SignInColumns.FirstFactorSuccessVerificationID)
	if err != nil {
		return nil, err
	}

	signInToken.Status = constants.StatusAccepted
	if err := a.signInTokenRepo.UpdateStatus(ctx, tx, signInToken); err != nil {
		return nil, err
	}

	return verification, nil
}

func (a TicketAttemptor) handleActorToken(
	ctx context.Context,
	tx database.Tx,
	claims ticket.Claims) (*model.Verification, error) {
	actorToken, err := a.actorTokenRepo.QueryByIDAndInstance(ctx, tx, claims.SourceID, claims.InstanceID)
	if err != nil {
		return nil, err
	}

	if actorToken == nil {
		return nil, ErrActorTokenNotFound
	} else if actorToken.Status == constants.StatusRevoked {
		return nil, ErrActorTokenRevoked
	} else if actorToken.Status == constants.StatusAccepted {
		return nil, ErrActorTokenAlreadyAccepted
	}

	if a.signIn == nil {
		return nil, ErrActorTokenUsedOutsideOfSignIn
	}

	user, err := a.userRepo.QueryByID(ctx, tx, actorToken.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrActorTokenSubjectNotFound
	}

	// We now need to find an identification for this user, so that
	// we can use it in the sign in object.
	// Remember that sign ins are bound to a particular identification
	// and not to a user.
	identificationID, err := a.findIdentificationForUser(ctx, tx, user, claims.InstanceID)
	if err != nil {
		return nil, err
	}

	// Create verification
	verification := &model.Verification{
		Verification: &sqbmodel.Verification{
			InstanceID: claims.InstanceID,
			Strategy:   constants.VSTicket,
			Attempts:   1,
			Token:      null.StringFrom(a.ticket),
		},
	}

	if err := a.verificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: creating new verification %+v for %s: %w",
			verification, a.ticket, err)
	}

	verification.IdentificationID = null.StringFrom(identificationID)
	if err := a.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: updating identification id to %s on verification %s: %w",
			identificationID, verification.ID, err)
	}

	a.signIn.IdentificationID = null.StringFrom(identificationID)
	a.signIn.FirstFactorCurrentVerificationID = null.StringFromPtr(nil)
	a.signIn.FirstFactorSuccessVerificationID = null.StringFrom(verification.ID)

	// Also update second factor verification id to bypass MFA.
	// Actor tokens bypass the whole sign in flow.
	a.signIn.SecondFactorSuccessVerificationID = null.StringFrom(verification.ID)

	a.signIn.ActorTokenID = null.StringFrom(actorToken.ID)

	err = a.signInRepo.Update(
		ctx, tx, a.signIn,
		sqbmodel.SignInColumns.IdentificationID,
		sqbmodel.SignInColumns.FirstFactorCurrentVerificationID,
		sqbmodel.SignInColumns.FirstFactorSuccessVerificationID,
		sqbmodel.SignInColumns.SecondFactorSuccessVerificationID,
		sqbmodel.SignInColumns.ActorTokenID)
	if err != nil {
		return nil, err
	}

	actorToken.Status = constants.StatusAccepted
	if err := a.actorTokenRepo.UpdateStatus(ctx, tx, actorToken); err != nil {
		return nil, err
	}

	return verification, nil
}

func (a TicketAttemptor) handleSAMLIdPInitiated(ctx context.Context, tx database.Tx, claims ticket.Claims) (*model.Verification, error) {
	// NOTE: this is security-critical, to mitigate replay attacks. Consume token in order not be able to reuse it
	cacheKey := claims.SAMLIdpInitiatedCacheKey()
	exists, err := a.cache.Exists(ctx, cacheKey)
	if err != nil {
		return nil, fmt.Errorf("ticket/attempt: check if cache key exists %s: %w", cacheKey, err)
	}
	if exists {
		return nil, fmt.Errorf("ticket/attempt: cache key %s already exists: %w", cacheKey, ErrTicketInvalid)
	}

	if err := a.cache.Set(ctx, cacheKey, claims.ID, time.Second*time.Duration(constants.ExpiryTimeSAMLIdpInitiated)); err != nil {
		return nil, fmt.Errorf("ticket/attempt: set cache key %s: %w", cacheKey, err)
	}

	samlConnection, err := a.samlConnectionRepo.QueryActiveByIDAndInstanceID(ctx, tx, claims.SourceID, claims.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("ticket/attempt: fetch active saml connection %s: %w", claims.SourceID, err)
	}
	if samlConnection == nil {
		return nil, fmt.Errorf("ticket/attempt: saml connection %s not found: %w", claims.SourceID, ErrSAMLIdpInitiatedActiveConnectionNotFound)
	}

	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: claims.InstanceID,
		Strategy:   constants.VSTicket,
		Attempts:   1,
		Token:      null.StringFrom(a.ticket),
	}}
	if err := a.verificationRepo.Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("ticket/attempt: creating new saml idp initiated verification: %w", err)
	}

	if a.signIn != nil {
		samlAccount, err := a.samlAccountService.QueryForUser(ctx, tx, samlConnection, claims.SAMLUser)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: query saml account for user %+v: %w", claims.SAMLUser, err)
		}

		var samlIdentification *model.Identification
		if samlAccount == nil {
			samlResults, err := a.samlAccountService.Create(ctx, tx, verification, claims.SAMLUser, samlConnection)
			if err != nil {
				return nil, fmt.Errorf("ticket/attempt: create saml account and identifications: %w", err)
			}

			samlIdentification = samlResults.SAMLIdentification
			samlIdentification.UserID = null.StringFrom(samlResults.EmailIdentification.UserID.String)
			if err = a.identificationRepo.UpdateUserID(ctx, tx, samlIdentification); err != nil {
				return nil, fmt.Errorf("ticket/attempt: update saml identification %+v: %w", samlIdentification, err)
			}
		} else {
			samlIdentification, err = a.identificationRepo.FindByIDAndInstance(ctx, tx, samlAccount.IdentificationID, a.env.Instance.ID)
			if err != nil {
				return nil, fmt.Errorf("ticket/attempt: fetch identification for saml account %+v: %w", samlAccount, err)
			}

			user, err := a.userRepo.FindByIDAndInstance(ctx, tx, samlIdentification.UserID.String, a.env.Instance.ID)
			if err != nil {
				return nil, fmt.Errorf("ticket/attempt: fetch user for saml identification %+v: %w", samlIdentification, err)
			}

			if samlConnection.SyncUserAttributes {
				userSettings := usersettings.NewUserSettings(a.env.AuthConfig.UserSettings)
				if err := a.samlAccountService.Update(ctx, tx, userSettings, a.env.Instance, user, samlAccount, claims.SAMLUser); err != nil {
					return nil, fmt.Errorf("ticket/attempt: update saml account %+v: %w", samlAccount, err)
				}
			}
		}

		a.signIn.SamlConnectionID = null.StringFrom(samlConnection.ID)
		a.signIn.IdentificationID = null.StringFrom(samlIdentification.ID)
		err = a.signInRepo.Update(ctx, tx, a.signIn, sqbmodel.SignInColumns.SamlConnectionID, sqbmodel.SignInColumns.IdentificationID)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: update sign in %+v: %w", a.signIn, err)
		}
	} else if a.signUp != nil {
		samlResults, err := a.samlAccountService.Create(ctx, tx, verification, claims.SAMLUser, samlConnection)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: create saml account and identifications: %w", err)
		}

		a.signUp.SamlConnectionID = null.StringFrom(samlConnection.ID)
		a.signUp.SuccessfulExternalAccountIdentificationID = null.StringFrom(samlResults.SAMLIdentification.ID)
		err = a.signUpRepo.Update(ctx, tx, a.signUp, sqbmodel.SignUpColumns.SamlConnectionID, sqbmodel.SignUpColumns.SuccessfulExternalAccountIdentificationID)
		if err != nil {
			return nil, fmt.Errorf("ticket/attempt: update sign up with saml connection and successful external identification: %w", err)
		}
	}

	return verification, nil
}

// findIdentificationForUser finds an identification that can be used
// for the given user.
// In order to find an identification, we're looking at the user's
// primary identifications in the following order:
// 1. email address
// 2. phone number
// 3. web3 wallet
// 4. username
var (
	errNoIdentificationForUser = errors.New("ticket: no identification for user")
)

func (a TicketAttemptor) findIdentificationForUser(ctx context.Context, tx database.Tx, user *model.User, instanceID string) (string, error) {
	if user.PrimaryEmailAddressID.Valid {
		return user.PrimaryEmailAddressID.String, nil
	} else if user.PrimaryPhoneNumberID.Valid {
		return user.PrimaryPhoneNumberID.String, nil
	} else if user.PrimaryWeb3WalletID.Valid {
		return user.PrimaryWeb3WalletID.String, nil
	}

	// try username
	usernameIdentifications, err := a.identificationRepo.FindAllByUserAndType(ctx, tx, instanceID, user.ID, constants.ITUsername)
	if err != nil {
		return "", err
	} else if len(usernameIdentifications) > 0 {
		return usernameIdentifications[0].ID, nil
	}

	// try OAuth
	// first find all OAuth strategies that can be used for authentication
	authenticatableOAuth := make([]string, 0)
	for _, socialProviderSettings := range a.env.AuthConfig.UserSettings.Social {
		if socialProviderSettings.Authenticatable {
			authenticatableOAuth = append(authenticatableOAuth, socialProviderSettings.Strategy)
		}
	}
	// if there are such strategies, try to find all user identifications that use any of these
	// strategies
	if len(authenticatableOAuth) > 0 {
		oauthIdentifications, err := a.identificationRepo.FindAllByUserAndVerifiedAndTypes(ctx, tx, user.ID, true, authenticatableOAuth...)
		if err != nil {
			return "", err
		}
		if len(oauthIdentifications) > 0 {
			return oauthIdentifications[0].ID, nil
		}
	}

	return "", fmt.Errorf("ticket/findIdentificationForUser: unable to find identification for user %s: %w",
		user.ID, errNoIdentificationForUser)
}

func (TicketAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrTicketExpired) {
		return apierror.TicketExpired()
	} else if errors.Is(err, ErrTicketInvalid) {
		return apierror.TicketInvalid()
	} else if errors.Is(err, ErrInstanceInvitationAlreadyAccepted) {
		return apierror.InvitationAlreadyAccepted()
	} else if errors.Is(err, ErrInstanceInvitationRevoked) {
		return apierror.RevokedInvitation()
	} else if errors.Is(err, ErrInstanceInvitationIdentificationAlreadyExists) {
		return apierror.InvitationAccountAlreadyExists()
	} else if errors.Is(err, ErrInstanceInvitationIdentificationNotFound) {
		return apierror.InvitationIdentificationNotExist()
	} else if errors.Is(err, ErrOrganizationInvitationRevoked) {
		return apierror.OrganizationInvitationRevoked()
	} else if errors.Is(err, ErrOrganizationInvitationAlreadyAccepted) {
		return apierror.OrganizationInvitationAlreadyAccepted()
	} else if errors.Is(err, ErrOrganizationInvitationIdentificationNotFound) {
		return apierror.OrganizationInvitationIdentificationNotExist()
	} else if errors.Is(err, ErrOrganizationInvitationIdentificationAlreadyExists) {
		return apierror.OrganizationInvitationIdentificationAlreadyExists()
	} else if errors.Is(err, ErrOrganizationInvitationNotFound) {
		return apierror.OrganizationInvitationNotFound(param.Ticket.Name)
	} else if errors.Is(err, ErrOrganizationInvitationToDeletedOrganization) {
		return apierror.OrganizationInvitationToDeletedOrganization()
	} else if errors.Is(err, ErrSignInTokenNotFound) {
		return apierror.SignInTokenCannotBeUsed()
	} else if errors.Is(err, ErrSignInTokenRevoked) {
		return apierror.SignInTokenRevoked()
	} else if errors.Is(err, ErrSignInTokenAlreadyAccepted) {
		return apierror.SignInTokenAlreadyUsed()
	} else if errors.Is(err, ErrSignInTokenUsedOutsideOfSignIn) {
		return apierror.SignInTokenCanBeUsedOnlyInSignIn()
	} else if errors.Is(err, ErrActorTokenNotFound) {
		return apierror.ActorTokenCannotBeUsed()
	} else if errors.Is(err, ErrActorTokenRevoked) {
		return apierror.ActorTokenRevoked()
	} else if errors.Is(err, ErrActorTokenAlreadyAccepted) {
		return apierror.ActorTokenAlreadyUsed()
	} else if errors.Is(err, ErrActorTokenUsedOutsideOfSignIn) {
		return apierror.ActorTokenCanBeUsedOnlyInSignIn()
	} else if errors.Is(err, ErrActorTokenSubjectNotFound) {
		return apierror.ActorTokenSubjectNotFound()
	} else if errors.Is(err, errNoIdentificationForUser) {
		return apierror.SignInNoIdentificationForUser()
	} else if errors.Is(err, ErrSAMLIdpInitiatedActiveConnectionNotFound) {
		return apierror.SAMLConnectionActiveNotFound(param.Ticket.Name)
	}
	return apierror.Unexpected(err)
}

// ticket errors
var (
	ErrTicketExpired = errors.New("ticket: expired")
	ErrTicketInvalid = errors.New("ticket: invalid token")

	// instance invitation errors
	ErrInstanceInvitationAlreadyAccepted             = errors.New("instanceInvitation: already accepted")
	ErrInstanceInvitationRevoked                     = errors.New("instanceInvitation: revoked")
	ErrInstanceInvitationIdentificationAlreadyExists = errors.New("instanceInvitation: refers to an existing identifier")
	ErrInstanceInvitationIdentificationNotFound      = errors.New("instanceInvitation: refers to non-existing identifier")

	// organization invitation errors
	ErrOrganizationInvitationIdentificationNotFound      = errors.New("organizationInvitation: refers to non-existing identifier")
	ErrOrganizationInvitationIdentificationAlreadyExists = errors.New("organizationInvitation: refers to an existing identifier")
	ErrOrganizationInvitationRevoked                     = errors.New("organizationInvitation: revoked")
	ErrOrganizationInvitationAlreadyAccepted             = errors.New("organizationInvitation: already accepted")
	ErrOrganizationInvitationNotFound                    = errors.New("organizationInvitation: doesn't exist")
	ErrOrganizationInvitationToDeletedOrganization       = errors.New("organizationInvitation: deleted organization")

	// sign in token errors
	ErrSignInTokenNotFound            = errors.New("signInToken: token doesn't exist anymore")
	ErrSignInTokenRevoked             = errors.New("signInToken: revoked")
	ErrSignInTokenAlreadyAccepted     = errors.New("signInToken: already accepted")
	ErrSignInTokenUsedOutsideOfSignIn = errors.New("signInToken: can only be used in sign in")

	// actor token errors
	ErrActorTokenNotFound            = errors.New("actorToken: token doesn't exist anymore")
	ErrActorTokenRevoked             = errors.New("actorToken: revoked")
	ErrActorTokenAlreadyAccepted     = errors.New("actorToken: already accepted")
	ErrActorTokenUsedOutsideOfSignIn = errors.New("actorToken: can only be used in sign in")
	ErrActorTokenSubjectNotFound     = errors.New("actorToken: subject not found")

	// SAML IdP-initiated errors
	ErrSAMLIdpInitiatedActiveConnectionNotFound = errors.New("samlIdpInitiated: active connection not found")
)
