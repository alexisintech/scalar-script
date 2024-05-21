package apierror

import (
	"net/http"
	"time"

	clerktime "clerk/pkg/time"
)

type UserLockoutStatus struct {
	Locked                        bool
	LockoutExpiresIn              *time.Duration
	VerificationAttemptsRemaining *int64
}

func UserLocked(userLockoutStatus *UserLockoutStatus, supportEmail *string) Error {
	mainErr := &mainError{
		shortMessage: "Account locked",
		code:         UserLockedCode,
	}

	longMessage := "Your account is locked."

	if userLockoutStatus.LockoutExpiresIn != nil {
		humanDuration := clerktime.HumanizeDuration(*userLockoutStatus.LockoutExpiresIn)
		longMessage += " You will be able to try again in " + humanDuration + "."
		mainErr.meta = &userLockoutMeta{
			LockoutExpiresInSeconds: int64(userLockoutStatus.LockoutExpiresIn.Seconds()),
		}
	}

	support := "support"

	if supportEmail != nil {
		support = *supportEmail
	}

	longMessage += " For more information, please contact " + support + "."

	mainErr.longMessage = longMessage

	return New(http.StatusForbidden, mainErr)
}
