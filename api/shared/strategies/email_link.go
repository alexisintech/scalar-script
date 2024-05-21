package strategies

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/comms"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/jwt"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	pkiutils "clerk/utils/pki"

	josejwt "github.com/go-jose/go-jose/v3/jwt"
	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type EmailLinkPreparer struct {
	clock          clockwork.Clock
	env            *model.Env
	identification *model.Identification

	redirectURL string
	sourceType  string
	sourceID    string

	commsService       *comms.Service
	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	verificationRepo   *repository.Verification
}

func NewEmailLinkPreparer(
	deps clerk.Deps,
	sourceType, sourceID string,
	redirectURL string,
	env *model.Env,
	identification *model.Identification,
) EmailLinkPreparer {
	return EmailLinkPreparer{
		clock:              deps.Clock(),
		env:                env,
		identification:     identification,
		redirectURL:        redirectURL,
		sourceType:         sourceType,
		sourceID:           sourceID,
		commsService:       comms.NewService(deps),
		identificationRepo: repository.NewIdentification(),
		signInRepo:         repository.NewSignIn(),
		signUpRepo:         repository.NewSignUp(),
		verificationRepo:   repository.NewVerification(),
	}
}

func (p EmailLinkPreparer) Identification() *model.Identification {
	return p.identification
}

func (p EmailLinkPreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	// check if there is an existing verification
	verification, err := p.findExistingVerification(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("prepare: finding existing verification for (sourceType=%s, sourceID=%s): %w",
			p.sourceType, p.sourceID, err)
	}

	if verification == nil || verification.Expired(p.clock) {
		verification, err = createVerification(ctx, tx, p.clock, &createVerificationParams{
			instanceID:       p.env.Instance.ID,
			strategy:         constants.VSEmailLink,
			identificationID: &p.identification.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("prepare: creating verification for email link: %w", err)
		}
	}

	if verification.Strategy != constants.VSEmailLink {
		return verification, fmt.Errorf("emailLink/attempt: was expecting %s strategy, found %s instead: %w",
			constants.VSEmailLink, verification.Strategy, ErrInvalidStrategyForVerification)
	}

	var devBrowserID string
	if devBrowser := requestingdevbrowser.FromContext(ctx); devBrowser != nil {
		devBrowserID = devBrowser.ID
	}
	claims := VerificationLinkTokenClaims{
		InstanceID:     p.env.Instance.ID,
		RedirectURL:    p.redirectURL,
		SourceType:     p.sourceType,
		SourceID:       p.sourceID,
		VerificationID: verification.ID,
		DevBrowserID:   devBrowserID,
	}
	ttl := time.Second * time.Duration(constants.ExpiryTimeTransactional)
	claims.Expiry = josejwt.NewNumericDate(p.clock.Now().UTC().Add(ttl))

	token, err := jwt.GenerateToken(p.env.Instance.PrivateKey, claims, p.env.Instance.KeyAlgorithm)
	if err != nil {
		return nil, fmt.Errorf("prepare: generate token for (%+v, %+v): %w", p, verification, err)
	}

	verification.Token = null.StringFrom(token)
	if err := p.verificationRepo.UpdateToken(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("prepare: updating token for email link verification %+v: %w",
			verification, err)
	}

	fapiURL := p.env.Domain.FapiURL()
	clerkJSVersion := clerkjs_version.FromContext(ctx)
	link, err := p.createVerificationLink(token, fapiURL, clerkJSVersion)
	if err != nil {
		return nil, fmt.Errorf("prepare: creating verification link for %s: %w",
			fapiURL, err)
	}

	if err = p.sendMagicLinkEmail(ctx, tx, link, ttl); err != nil {
		return nil, fmt.Errorf("prepare: sending email link for %+v: %w",
			p.identification, err)
	}

	return verification, nil
}

func (p EmailLinkPreparer) findExistingVerification(ctx context.Context, exec database.Executor) (*model.Verification, error) {
	var verificationID string
	switch p.sourceType {
	case constants.OSTSignIn:
		signIn, err := p.signInRepo.FindByID(ctx, exec, p.sourceID)
		if err != nil {
			return nil, fmt.Errorf("prepare: fetching sign in source with id %s: %w", p.sourceID, err)
		}
		if !signIn.FirstFactorCurrentVerificationID.Valid {
			return nil, nil
		}

		verificationID = signIn.FirstFactorCurrentVerificationID.String
	case constants.OSTSignUp:
		signUp, err := p.signUpRepo.FindByID(ctx, exec, p.sourceID)
		if err != nil {
			return nil, fmt.Errorf("prepare: fetching sign up source with id %s: %w", p.sourceID, err)
		}

		if !signUp.EmailAddressID.Valid {
			return nil, nil
		}

		identification, err := p.identificationRepo.FindByID(ctx, exec, signUp.EmailAddressID.String)
		if err != nil {
			return nil, fmt.Errorf("prepare: fetching identification with id %s: %w", signUp.EmailAddressID.String, err)
		}

		if !identification.VerificationID.Valid {
			return nil, nil
		}
		verificationID = identification.VerificationID.String
	case constants.OSTUser:
		identification, err := p.identificationRepo.FindByID(ctx, exec, p.sourceID)
		if err != nil {
			return nil, fmt.Errorf("prepare: fetching identification with id %s: %w", p.sourceID, err)
		}

		if !identification.VerificationID.Valid {
			return nil, nil
		}
		verificationID = identification.VerificationID.String
	}

	verification, err := p.verificationRepo.FindByID(ctx, exec, verificationID)
	if err != nil {
		return nil, fmt.Errorf("prepare: fetching verification with id %s: %w", verificationID, err)
	}

	if verification.Strategy != constants.VSEmailLink {
		// there is already a verification but with different strategy, so we need to create a new one
		return nil, nil
	}
	return verification, nil
}

func (p EmailLinkPreparer) createVerificationLink(token, fapiURL, clerkJSVersion string) (string, error) {
	link, err := url.Parse(fapiURL)
	if err != nil {
		return "", err
	}

	link = link.JoinPath("/v1/verify")
	query := link.Query()
	query.Set("token", token)
	if clerkJSVersion != "" {
		query.Set(param.ClerkJSVersion, clerkJSVersion)
	}
	link.RawQuery = query.Encode()
	return link.String(), nil
}

func (p EmailLinkPreparer) sendMagicLinkEmail(ctx context.Context, tx database.Tx, link string, ttl time.Duration) error {
	deviceActivity := activity.FromContext(ctx)

	switch p.sourceType {
	case constants.OSTSignIn:
		return p.commsService.SendMagicLinkSignInEmail(ctx, tx, p.identification, link, ttl, p.sourceType, p.sourceID, p.env, deviceActivity)
	case constants.OSTSignUp:
		return p.commsService.SendMagicLinkSignUpEmail(ctx, tx, p.identification, link, ttl, p.sourceType, p.sourceID, p.env, deviceActivity)
	case constants.OSTUser:
		return p.commsService.SendMagicLinkUserProfileEmail(ctx, tx, p.identification, link, ttl, p.sourceType, p.sourceID, p.env, deviceActivity)
	}
	return nil
}

type VerificationLinkTokenClaims struct {
	josejwt.Claims

	InstanceID     string `json:"iid"`
	RedirectURL    string `json:"rurl,omitempty"`
	SourceType     string `json:"st"`
	SourceID       string `json:"sid"`
	VerificationID string `json:"vid"`
	DevBrowserID   string `json:"dev,omitempty"`
}

// ParseVerificationLinkToken parses the provided token with the help of the
// provided public key. It returns the VerificationLinkTokenClaims for the
// token.
func ParseVerificationLinkToken(token, pubKey, keyAlgo string, clock clockwork.Clock) (VerificationLinkTokenClaims, error) {
	var claims VerificationLinkTokenClaims

	if token == "" {
		return claims, fmt.Errorf("ParseVerificationLinkToken: token is blank")
	}

	pubkey, err := pkiutils.LoadPublicKey([]byte(pubKey))
	if err != nil {
		return claims, clerkerrors.WithStacktrace("ParseVerificationLinkToken: unable to parse instance public key: %w", err)
	}

	err = jwt.Verify(token, pubkey, &claims, clock, keyAlgo)
	if err != nil {
		return claims, clerkerrors.WithStacktrace("ParseVerificationLinkToken: cannot get claims: %w", err)
	}

	return claims, nil
}

// EmailLinkAttemptor attempts to complete verifications with link tokens.
type EmailLinkAttemptor struct {
	instanceID     string
	verificationID string

	verificationService *verifications.Service
	verificationRepo    *repository.Verification
}

// NewEmailLinkAttemptor returns a EmailLinkAttemptor for the provided verification
// and instance ID.
func NewEmailLinkAttemptor(verificationID, instanceID string, clock clockwork.Clock) EmailLinkAttemptor {
	return EmailLinkAttemptor{
		instanceID:          instanceID,
		verificationID:      verificationID,
		verificationService: verifications.NewService(clock),
		verificationRepo:    repository.NewVerification(),
	}
}

// Attempt retrieves the model.Verification specified by VerificationID
// and validates that it's ready for verification.
func (v EmailLinkAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	verification, err := v.verificationRepo.FindByID(ctx, tx, v.verificationID)
	if err != nil {
		return nil, err
	}

	if err := checkVerificationStatus(ctx, tx, v.verificationService, verification); err != nil {
		return verification, err
	}

	return verification, nil
}

func (EmailLinkAttemptor) ToAPIError(err error) apierror.Error {
	return toAPIErrors(err)
}
