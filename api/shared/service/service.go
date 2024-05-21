package service

// Current file is intended for shared service interfaces.
// This enables us to inject services with a reduced risk of encountering cyclic imports.

import (
	"context"

	"clerk/model"
	"clerk/utils/database"
)

type UserLockout interface {
	IncrementFailedVerificationAttempts(context.Context, database.Executor, *model.Env, *model.User) error
}
