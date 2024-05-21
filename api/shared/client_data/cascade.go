package client_data

import (
	"context"

	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type DeleteCascader struct {
	signInRepo            *repository.SignIn
	signUpRepo            *repository.SignUp
	integrationsRepo      *repository.Integrations
	verificationsRepo     *repository.Verification
	syncNoncesRepo        *repository.SyncNonces
	sessionActivitiesRepo *repository.SessionActivities
	db                    database.Database
}

func NewDeleteCascader(deps clerk.Deps) *DeleteCascader {
	return &DeleteCascader{
		db:                    deps.DB(),
		signInRepo:            repository.NewSignIn(),
		signUpRepo:            repository.NewSignUp(),
		integrationsRepo:      repository.NewIntegrations(),
		verificationsRepo:     repository.NewVerification(),
		syncNoncesRepo:        repository.NewSyncNonces(),
		sessionActivitiesRepo: repository.NewSessionActivities(),
	}
}

func (d *DeleteCascader) OnSessionDeleted(ctx context.Context, instanceID, _, sessionID string) error {
	if err := d.signInRepo.DeleteAllByInstanceIDAndCreatedSessionID(ctx, d.db, instanceID, sessionID); err != nil {
		return err
	}
	if err := d.sessionActivitiesRepo.DeleteAllBySessionID(ctx, d.db, sessionID); err != nil {
		return err
	}
	return nil
}

func (d *DeleteCascader) OnClientDeleted(ctx context.Context, instanceID, clientID string) error {
	// Replicate the previous ON DELETE CASCADE behavior
	if err := d.signInRepo.DeleteAllByInstanceIDAndClientID(ctx, d.db, instanceID, clientID); err != nil {
		return err
	}
	if err := d.signUpRepo.DeleteAllByInstanceIDAndClientID(ctx, d.db, instanceID, clientID); err != nil {
		return err
	}
	if err := d.integrationsRepo.DeleteAllByInstanceIDAndClientID(ctx, d.db, instanceID, clientID); err != nil {
		return err
	}
	if err := d.syncNoncesRepo.DeleteAllByInstanceIDAndClientID(ctx, d.db, instanceID, clientID); err != nil {
		return err
	}
	// Replicate the previous ON DELETE SET NULL behavior
	if err := d.verificationsRepo.UpdateAllByInstanceIDAndClientIDSetVerifiedAtClientNull(ctx, d.db, instanceID, clientID); err != nil {
		return err
	}
	return nil
}
