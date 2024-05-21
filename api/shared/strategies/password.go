package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/hibp"
	"clerk/pkg/hash"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/volatiletech/null/v8"
)

var (
	HibpClient         hibp.Client
	ErrInvalidPassword = errors.New("verification: invalid password")
	ErrPwnedPassword   = errors.New("verification: pwned password")
)

func init() {
	HibpClient = hibp.NewClient()
}

type PasswordAttemptor struct {
	userPasswordDigest string
	userPasswordHasher string
	password           string
	instanceID         string
	userSettings       usersettingsmodel.UserSettings

	env  *model.Env
	user *model.User

	userRepo         *repository.Users
	verificationRepo *repository.Verification
}

type PasswordAttemptorParams struct {
	Env      *model.Env
	User     *model.User
	Password string
}

func NewPasswordAttemptor(params PasswordAttemptorParams) PasswordAttemptor {
	return PasswordAttemptor{
		userPasswordDigest: params.User.PasswordDigest.String,
		userPasswordHasher: params.User.PasswordHasher.String,
		password:           params.Password,
		instanceID:         params.Env.Instance.ID,
		userSettings:       params.Env.AuthConfig.UserSettings,
		env:                params.Env,
		user:               params.User,
		userRepo:           repository.NewUsers(),
		verificationRepo:   repository.NewVerification(),
	}
}

func (v PasswordAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID: v.instanceID,
		Strategy:   constants.VSPassword,
		Attempts:   1,
	}}

	err := v.verificationRepo.Insert(ctx, tx, verification)
	if err != nil {
		return nil, fmt.Errorf("verifications/attempt: inserting new verification %+v: %w", verification, err)
	}

	if matches, err := hash.Compare(v.userPasswordHasher, v.password, v.userPasswordDigest); err != nil {
		return verification, fmt.Errorf("password/attempt: error while trying to compare password with digest %s: %w",
			v.userPasswordDigest, err)
	} else if !matches {
		return verification, fmt.Errorf("password/attempt: given password does not match user's password digest %s: %w",
			v.userPasswordDigest, ErrInvalidPassword)
	}

	if err = v.migrateInsecureHashersToBcrypt(ctx, tx); err != nil {
		return nil, fmt.Errorf("password/attempt: updating insecure user to false %s: %w", v.user.ID, err)
	}

	if !v.userSettings.PasswordSettings.DisableHIBP && v.userSettings.PasswordSettings.EnforceHIBPOnSignIn {
		// If password matches, but is now on the compromised list, reject this sign-in attempt
		if pwned, err := HibpClient.CheckIfPasswordPwned(ctx, v.password); err != nil {
			return nil, fmt.Errorf("password/attempt: checking if user password is compromised %s: %w", v.user.ID, err)
		} else if pwned {
			return verification, ErrPwnedPassword
		}
	}

	return verification, nil
}

func (PasswordAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidPassword) {
		return apierror.FormPasswordIncorrect(param.Password.Name)
	} else if errors.Is(err, ErrPwnedPassword) {
		return apierror.FormPwnedPassword(param.Password.Name, true)
	}

	return apierror.Unexpected(err)
}

func (v PasswordAttemptor) migrateInsecureHashersToBcrypt(ctx context.Context, tx database.Tx) error {
	hasher := hash.GetHasher(v.userPasswordHasher)
	if hasher == nil || !hasher.ShouldMigrateToBcrypt() {
		return nil
	}

	bcryptDigest, err := hash.GenerateBcryptHash(v.password)
	if err != nil {
		return err
	}

	v.user.PasswordDigest = null.StringFrom(bcryptDigest)
	v.user.PasswordHasher = null.StringFrom(hash.Bcrypt)
	return v.userRepo.Update(ctx, tx, v.user, sqbmodel.UserColumns.PasswordDigest, sqbmodel.UserColumns.PasswordHasher)
}
