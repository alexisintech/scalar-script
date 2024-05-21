package emails

import (
	"context"
	"encoding/json"
	"fmt"

	"clerk/api/shared/events"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	gueClient *gue.Client

	eventService *events.Service
	emailsRepo   *repository.Email
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		gueClient:    deps.GueClient(),
		eventService: events.NewService(deps),
		emailsRepo:   repository.NewEmail(),
	}
}

// Send "sends" an email by creating a new Email record in the database.
func (s *Service) Send(ctx context.Context, tx database.Tx, emailData *model.EmailData, env *model.Env) (*model.Email, error) {
	if env.Instance.IsDevelopmentOrStaging() && !cenv.IsBeforeCutoff(cenv.StopDevInProdCutOffDateEpochTime, env.Instance.CreatedAt) {
		emailData.PrependTagToSubject(env.Instance.EnvironmentType)
	}
	newEmail := &model.Email{
		Email: &sqbmodel.Email{
			InstanceID:       env.AuthConfig.InstanceID,
			Slug:             null.StringFromPtr(emailData.Slug),
			FromEmailName:    emailData.FromEmailName,
			ReplyToEmailName: null.StringFromPtr(emailData.ReplyToEmailName),
			Subject:          emailData.Subject,
			Body:             emailData.Body,
			Status:           string(constants.EmailMessageStatusQueued),
			DeliveredByClerk: emailData.DeliveredByClerk,
		},
	}

	if emailData.Data != nil {
		dataJSON, err := json.Marshal(emailData.Data)
		if err != nil {
			return nil, err
		}

		newEmail.Data = dataJSON
	}

	if emailData.Identification != nil {
		emailAddress := emailData.Identification.EmailAddress()
		if emailAddress == nil {
			return nil, fmt.Errorf("email/send: expected email address identification, found %s instead",
				emailData.Identification.Type)
		}

		newEmail.EmailAddressID = null.StringFrom(emailData.Identification.ID)
		newEmail.UserID = emailData.Identification.UserID
		newEmail.ToEmailAddress = *emailAddress
	} else if emailData.ToEmailAddress != nil {
		newEmail.ToEmailAddress = *emailData.ToEmailAddress
	}

	if emailData.BodyPlaintext != "" {
		newEmail.BodyPlain = null.StringFrom(emailData.BodyPlaintext)
	}

	if err := s.emailsRepo.Insert(ctx, tx, newEmail); err != nil {
		return nil, fmt.Errorf("email/send: error insert %+v: %w", newEmail, err)
	}

	if err := s.enqueueJob(ctx, tx, newEmail, emailData); err != nil {
		return nil, fmt.Errorf("email/send: error enqueuing job (%s, %s): %w", newEmail, emailData, err)
	}

	// Also send event & webhook for email
	var userID *string
	if emailData.Identification != nil {
		userID = emailData.Identification.UserID.Ptr()
	}

	if err := s.eventService.EmailCreated(ctx, tx, env.Instance, eventPayload(newEmail), userID); err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and continue
		sentryclerk.CaptureException(ctx, err)
	}

	return newEmail, nil
}

func (s *Service) enqueueJob(ctx context.Context, tx database.Tx, email *model.Email, data *model.EmailData) error {
	if !email.DeliveredByClerk {
		return nil
	}

	return jobs.SendEmail(ctx, s.gueClient, jobs.SendEmailArgs{
		InstanceID: email.InstanceID,
		EmailID:    email.ID,
		CustomFlow: data.CustomFlow,
	}, jobs.WithTx(tx))
}

// FIXME
// This is a duplicate of:
// pkg/serialize/email
// However using it here introduces an import cycle

type emailEventPayload struct {
	ID               string          `json:"id"`
	Object           string          `json:"object"`
	Slug             null.String     `json:"slug"`
	FromEmailName    string          `json:"from_email_name"`
	ReplyToEmailName null.String     `json:"reply_to_email_name,omitempty"`
	ToEmailAddress   string          `json:"to_email_address,omitempty"`
	EmailAddressID   null.String     `json:"email_address_id,omitempty"`
	UserID           null.String     `json:"user_id,omitempty"`
	Subject          string          `json:"subject,omitempty"`
	Body             string          `json:"body,omitempty"`
	BodyPlain        null.String     `json:"body_plain,omitempty"`
	Status           string          `json:"status,omitempty"`
	Data             json.RawMessage `json:"data"`
	DeliveredByClerk bool            `json:"delivered_by_clerk"`
}

func eventPayload(email *model.Email) *emailEventPayload {
	return &emailEventPayload{
		Object:           "email",
		ID:               email.ID,
		Slug:             email.Slug,
		FromEmailName:    email.FromEmailName,
		ReplyToEmailName: email.ReplyToEmailName,
		ToEmailAddress:   email.ToEmailAddress,
		EmailAddressID:   email.EmailAddressID,
		UserID:           email.UserID,
		Subject:          email.Subject,
		Body:             email.Body,
		BodyPlain:        email.BodyPlain,
		Status:           email.Status,
		Data:             json.RawMessage(email.Data),
		DeliveredByClerk: email.DeliveredByClerk,
	}
}

// SendHypeStats is responsible for enqueuing a job to send a hype stats email.
// It will first check if a matching email has already been created to avoid
// enqueue jobs that will result in duplicate emails.
//
// It is not responsible for sending the email itself.
//
// This is not an event that customers will need to subscribe to. So
// there's no need to persist to the events table, or send a webhook.
func (s *Service) SendHypeStats(ctx context.Context, db database.Database, emailData *model.EmailData, instanceID string) (*model.Email, error) {
	dataJSON, err := json.Marshal(emailData.Data)
	if err != nil {
		return nil, err
	}

	prev, err := s.emailsRepo.QueryByInstanceEmailAndData(ctx, db, instanceID, *emailData.ToEmailAddress, dataJSON)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("email/SendHypeStats: error finding previous email: %w", err)
	}
	if prev != nil {
		log.Warning(ctx, "email/SendHypeStats: email already enqueued for data=(%s): id=(%s). Skipping...", emailData.Data, prev.ID)
		return prev, nil
	}

	log.Debug(ctx, "email/SendHypeStats: no previous email found for data=(%s) Will send...", emailData.Data)

	email := &model.Email{
		Email: &sqbmodel.Email{
			ToEmailAddress:   *emailData.ToEmailAddress,
			InstanceID:       instanceID,
			Slug:             null.StringFromPtr(emailData.Slug),
			FromEmailName:    emailData.FromEmailName,
			Subject:          emailData.Subject,
			Body:             emailData.Body,
			Status:           string(constants.EmailMessageStatusQueued),
			DeliveredByClerk: emailData.DeliveredByClerk,
			Data:             dataJSON,
		},
	}

	// start a transaction so we can rollback the email insertion if any error occurs
	txErr := db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.emailsRepo.Insert(ctx, tx, email)
		if err != nil {
			return true, fmt.Errorf("email/SendHypeStats: failed to insert: %w", err)
		}

		err = jobs.SendHypeStatsEmail(ctx, s.gueClient, jobs.SendHypeStatsEmailArgs{
			EmailID: email.ID,
		})
		if err != nil {
			return true, fmt.Errorf("email/SendHypeStats: error enqueuing job %w", err)
		}
		return false, nil
	})
	if txErr != nil {
		return nil, txErr
	}

	log.Info(ctx, "email/SendHypeStats: enqueued job for email=(%s)", email.ID)

	return email, nil
}
