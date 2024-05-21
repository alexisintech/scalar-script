package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/emails"
	"clerk/api/shared/sms"
	shtemplates "clerk/api/shared/templates"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/templates"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock

	// services
	emailService *emails.Service
	smsService   *sms.Service
	templateSvc  *shtemplates.Service

	// repositories
	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	userRepo           *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock: deps.Clock(),

		emailService: emails.NewService(deps),
		smsService:   sms.NewService(deps),
		templateSvc:  shtemplates.NewService(deps.Clock()),

		identificationRepo: repository.NewIdentification(),
		signInRepo:         repository.NewSignIn(),
		signUpRepo:         repository.NewSignUp(),
		userRepo:           repository.NewUsers(),
	}
}

func (s *Service) SendAffiliationCodeEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	deviceActivity *model.SessionActivity,
	orgDomainName, affiliationEmailAddress, code string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.AffiliationCodeSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendAffiliationCodeEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	deviceActivityData := s.templateSvc.GetDeviceActivityData(deviceActivity)

	data := templates.AffiliationCodeEmailData{
		CommonEmailData:        commonEmailData,
		DeviceActivityData:     deviceActivityData,
		OTPCode:                code,
		OrganizationDomainName: orgDomainName,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &affiliationEmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendAffiliationCodeEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

func (s *Service) SendVerificationCodeEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	code string,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.VerificationCodeSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendVerificationCodeEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	commonVerificationData, err := s.templateSvc.GetCommonVerificationData(ctx, tx, sourceType, sourceID, env.Instance.ID)
	if err != nil {
		return err
	}

	deviceActivityData := s.templateSvc.GetDeviceActivityData(deviceActivity)

	data := templates.VerificationCodeEmailData{
		CommonEmailData:        commonEmailData,
		OTPCode:                code,
		CommonVerificationData: commonVerificationData,
		DeviceActivityData:     deviceActivityData,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, emailAddress, nil)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendVerificationCodeEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

func (s *Service) SendInvitationEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	invitation *model.Invitation,
	actionURL string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.InvitationSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendInvitationEmail: populating common email data for instance with id %s: %w",
			env.Instance.ID, err)
	}

	inviterName, err := s.getInviterName(ctx, tx, env.Instance.ApplicationID)
	if err != nil {
		return fmt.Errorf("sendInvitationEmail: get inviter name for application %s: %w", env.Instance.ApplicationID, err)
	}

	data := templates.InvitationEmailData{
		CommonEmailData:      commonEmailData,
		CommonInvitationData: templates.NewCommonInvitationData(inviterName, actionURL),
		Invitation:           templates.InvitationToTemplateData(invitation),
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &invitation.EmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendInvitationEmail: sending email data %+v: %w",
			emailData, err)
	}

	return nil
}

func (s *Service) SendInvitationSMS(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	phoneNumber, actionURL string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTSMS, constants.InvitationSlug)
	if err != nil {
		return err
	}

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendInvitationSMS: populating common SMS data for instance with id %s: %w", env.Instance.ID, err)
	}

	inviterName, err := s.getInviterName(ctx, tx, env.Instance.ApplicationID)
	if err != nil {
		return fmt.Errorf("sendInvitationSMS: get inviter name for application %s: %w", env.Instance.ApplicationID, err)
	}
	data := templates.InvitationSMSData{
		CommonSMSData:        commonSMSData,
		CommonInvitationData: templates.NewCommonInvitationData(inviterName, actionURL),
	}

	smsData, err := templates.RenderSMS(data, template, nil, &phoneNumber)
	if err != nil {
		return err
	}

	_, err = s.smsService.Send(ctx, tx, smsData, env)
	if errors.Is(err, apierror.QuotaExceeded()) {
		return apierror.QuotaExceeded()
	} else if err != nil {
		return fmt.Errorf("sendInvitationSMS: sending SMS data %+v: %w",
			smsData, err)
	}

	return nil
}

type EmailOrganizationInvitation struct {
	Organization *model.Organization
	Invitation   *model.OrganizationInvitation
	InviterName  string
	ActionURL    string
}

func (s *Service) SendOrganizationInvitationEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	params EmailOrganizationInvitation,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.OrganizationInvitationSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationInvitationEmail: populating common email data for instance with id %s: %w",
			env.Instance.ID, err)
	}

	data := templates.OrganizationInvitationEmailData{
		CommonEmailData:      commonEmailData,
		CommonInvitationData: templates.NewCommonInvitationData(params.InviterName, params.ActionURL),
		Invitation: templates.OrganizationInvitationData{
			PublicMetadata: json.RawMessage(params.Invitation.PublicMetadata),
		},
		Organization: orgToOrganizationData(params.Organization),
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &params.Invitation.EmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationInvitationEmail: sending email data %+v: %w",
			emailData, err)
	}

	return nil
}

type EmailOrganizationJoined struct {
	Organization *model.Organization
	EmailAddress string
}

func (s *Service) SendOrganizationJoinedEmail(ctx context.Context, tx database.Tx, env *model.Env, params EmailOrganizationJoined) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.OrganizationJoinedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationJoinedEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	data := templates.OrganizationJoinedEmailData{
		CommonEmailData: commonEmailData,
		Organization:    orgToOrganizationData(params.Organization),
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &params.EmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationJoinedEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

type EmailOrganizationMembershipRequested struct {
	Organization  *model.Organization
	EmailAddress  string
	ToEmailIdents []*model.Identification
}

func (s *Service) SendOrganizationMembershipRequestedEmails(ctx context.Context, tx database.Tx, env *model.Env, params EmailOrganizationMembershipRequested) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.OrganizationMembershipRequestedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationMembershipRequestedEmails: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	data := templates.OrganizationMembershipRequestedEmailData{
		CommonEmailData: commonEmailData,
		EmailAddress:    params.EmailAddress,
		Organization:    orgToOrganizationData(params.Organization),
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	for _, emailIdent := range params.ToEmailIdents {
		emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, emailIdent.EmailAddress())
		if err != nil {
			return err
		}
		if env.Instance.IsDevelopmentOrStaging() && !cenv.IsBeforeCutoff(cenv.StopDevInProdCutOffDateEpochTime, env.Instance.CreatedAt) {
			emailData.PrependTagToSubject(env.Instance.EnvironmentType)
		}

		_, err = s.emailService.Send(ctx, tx, emailData, env)
		if err != nil {
			return fmt.Errorf("sendOrganizationMembershipRequestedEmails: sending email data %+v: %w", emailData, err)
		}
	}

	return nil
}

type EmailOrganizationInvitationAccepted struct {
	Organization  *model.Organization
	EmailAddress  string
	ToEmailIdents []*model.Identification
}

func (s *Service) SendOrganizationInvitationAcceptedEmails(ctx context.Context, tx database.Tx, env *model.Env, params EmailOrganizationInvitationAccepted) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.OrganizationInvitationAcceptedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendOrganizationInvitationAcceptedEmails: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	data := templates.OrganizationInvitationAcceptedEmailData{
		CommonEmailData: commonEmailData,
		EmailAddress:    params.EmailAddress,
		Organization:    orgToOrganizationData(params.Organization),
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	for _, emailIdent := range params.ToEmailIdents {
		emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, emailIdent.EmailAddress())
		if err != nil {
			return err
		}

		_, err = s.emailService.Send(ctx, tx, emailData, env)
		if err != nil {
			return fmt.Errorf("sendOrganizationInvitationAcceptedEmails: sending email data %+v: %w", emailData, err)
		}
	}

	return nil
}

type EmailPasswordChanged struct {
	GreetingName        string
	PrimaryEmailAddress string
}

func (s *Service) SendPasswordChangedEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	params EmailPasswordChanged,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.PasswordChangedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendPasswordChangedEmail: populating common email data for instance with id %s: %w",
			env.Instance.ID, err)
	}

	data := templates.PasswordChangedEmailData{
		CommonEmailData:     commonEmailData,
		GreetingName:        params.GreetingName,
		PrimaryEmailAddress: params.PrimaryEmailAddress,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &params.PrimaryEmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendPasswordChangedEmail: sending email data %+v: %w",
			emailData, err)
	}

	return nil
}

func (s *Service) SendPasswordChangedSMS(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	phoneNumber string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTSMS, constants.PasswordChangedSlug)
	if err != nil {
		return err
	}

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendPasswordChangedSMS: populating common sms data for instance with id %s: %w",
			env.Instance.ID, err)
	}

	smsData, err := templates.RenderSMS(commonSMSData, template, nil, &phoneNumber)
	if err != nil {
		return err
	}

	_, err = s.smsService.Send(ctx, tx, smsData, env)
	if err != nil {
		return fmt.Errorf("sendPasswordChangedSMS: sending sms data %+v: %w",
			smsData, err)
	}

	return nil
}

type EmailPasswordRemoved struct {
	GreetingName        string
	PrimaryEmailAddress string
}

func (s *Service) SendPasswordRemovedEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	params EmailPasswordRemoved,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.PasswordRemovedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendPasswordRemovedEmail: populating common email data for instance with id %s: %w",
			env.Instance.ID, err)
	}

	data := templates.PasswordRemovedEmailData{
		CommonEmailData:     commonEmailData,
		GreetingName:        params.GreetingName,
		PrimaryEmailAddress: params.PrimaryEmailAddress,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &params.PrimaryEmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendPasswordRemovedEmail: sending email data %+v: %w",
			emailData, err)
	}

	return nil
}

type EmailPrimaryEmailAddressChanged struct {
	PreviousEmailAddress string
	NewEmailAddress      string
}

func (s *Service) SendPrimaryEmailAddressChangedEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	params EmailPrimaryEmailAddressChanged,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.PrimaryEmailAddressChangedSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("SendPrimaryEmailAddressChangedEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	emailData, err := templates.RenderEmail(
		ctx,
		templates.PrimaryEmailAddressChangedEmailData{
			CommonEmailData: commonEmailData,
			NewEmailAddress: params.NewEmailAddress,
		},
		template,
		s.templateSvc.FromEmailName(template, env.Instance),
		nil,
		&params.PreviousEmailAddress,
	)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("SendPrimaryEmailAddressChangedEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

func (s *Service) SendResetPasswordCodeEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	code string,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, constants.ResetPasswordCodeSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendResetPasswordCodeEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	commonVerificationData, err := s.templateSvc.GetCommonVerificationData(ctx, tx, sourceType, sourceID, env.Instance.ID)
	if err != nil {
		return err
	}

	deviceActivityData := s.templateSvc.GetDeviceActivityData(deviceActivity)

	data := templates.ResetPasswordCodeEmailData{
		CommonEmailData:        commonEmailData,
		OTPCode:                code,
		CommonVerificationData: commonVerificationData,
		DeviceActivityData:     deviceActivityData,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, emailAddress, nil)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendVerificationCodeEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

func (s *Service) SendResetPasswordCodeSMS(
	ctx context.Context,
	tx database.Tx,
	phoneNumber *model.Identification,
	code string,
	sourceType, sourceID string,
	env *model.Env,
	verificationID string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTSMS, constants.ResetPasswordCodeSlug)
	if err != nil {
		return err
	}

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendResetPasswordCodeSMS: populating common SMS data for instance with id %s: %w", env.Instance.ID, err)
	}

	commonVerificationData, err := s.templateSvc.GetCommonVerificationData(ctx, tx, sourceType, sourceID, env.Instance.ID)
	if err != nil {
		return err
	}

	data := templates.ResetPasswordCodeSMSData{
		CommonSMSData:          commonSMSData,
		CommonVerificationData: commonVerificationData,
		OTPCode:                code,
	}

	smsData, err := templates.RenderSMS(data, template, phoneNumber, nil)
	if err != nil {
		return err
	}

	smsData.VerificationID = &verificationID

	_, err = s.smsService.Send(ctx, tx, smsData, env)
	if err != nil {
		return fmt.Errorf("sendResetPasswordCodeSMS: sending SMS data %+v: %w", smsData, err)
	}

	return nil
}

// SendVerificationCodeSMS sends an SMS OTP formatted according to the
// origin-bound spec (see https://wicg.github.io/sms-one-time-codes/).
func (s *Service) SendVerificationCodeSMS(
	ctx context.Context,
	tx database.Tx,
	phoneNumber *model.Identification,
	code string,
	sourceType, sourceID string,
	env *model.Env,
	verificationID string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTSMS, constants.VerificationCodeSlug)
	if err != nil {
		return err
	}

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendVerificationCodeSMS: populating common SMS data for instance with id %s: %w", env.Instance.ID, err)
	}

	commonVerificationData, err := s.templateSvc.GetCommonVerificationData(ctx, tx, sourceType, sourceID, env.Instance.ID)
	if err != nil {
		return err
	}

	data := templates.VerificationCodeSMSData{
		CommonSMSData:          commonSMSData,
		CommonVerificationData: commonVerificationData,
		OTPCode:                code,
	}

	smsData, err := templates.RenderSMS(data, template, phoneNumber, nil)
	if err != nil {
		return err
	}

	smsData.VerificationID = &verificationID

	_, err = s.smsService.Send(ctx, tx, smsData, env)
	if err != nil {
		return fmt.Errorf("sendVerificationCodeSMS: sending SMS data %+v: %w", smsData, err)
	}

	return nil
}

func (s *Service) SendMagicLinkSignInEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	link string,
	ttl time.Duration,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
) error {
	return s.sendMagicLinkEmail(ctx, tx, emailAddress, link, ttl, sourceType, sourceID, env, deviceActivity, constants.MagicLinkSignInSlug)
}

func (s *Service) SendMagicLinkSignUpEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	link string,
	ttl time.Duration,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
) error {
	return s.sendMagicLinkEmail(ctx, tx, emailAddress, link, ttl, sourceType, sourceID, env, deviceActivity, constants.MagicLinkSignUpSlug)
}

func (s *Service) SendMagicLinkUserProfileEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	link string,
	ttl time.Duration,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
) error {
	return s.sendMagicLinkEmail(ctx, tx, emailAddress, link, ttl, sourceType, sourceID, env, deviceActivity, constants.MagicLinkUserProfileSlug)
}

func (s *Service) sendMagicLinkEmail(
	ctx context.Context,
	tx database.Tx,
	emailAddress *model.Identification,
	link string,
	ttl time.Duration,
	sourceType, sourceID string,
	env *model.Env,
	deviceActivity *model.SessionActivity,
	templateSlug string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, templateSlug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendMagicLinkEmail: populating common email data for instance with id %s: %w", env.Instance.ID, err)
	}

	commonVerificationData, err := s.templateSvc.GetCommonVerificationData(ctx, tx, sourceType, sourceID, env.Instance.ID)
	if err != nil {
		return err
	}

	deviceActivityData := s.templateSvc.GetDeviceActivityData(deviceActivity)

	data := templates.MagicLinkEmailData{
		CommonEmailData:        commonEmailData,
		CommonVerificationData: commonVerificationData,
		DeviceActivityData:     deviceActivityData,
		MagicLink:              link,
		TTLMinutes:             fmt.Sprintf("%.0f", ttl.Minutes()),
		IsSignIn:               sourceType == constants.OSTSignIn,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, emailAddress, nil)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendMagicLinkEmail: sending email data %+v: %w", emailData, err)
	}

	return nil
}

// Get the application owner's name, if app is owned by a user.
func (s *Service) getInviterName(ctx context.Context, exec database.Executor, appID string) (string, error) {
	inviterName := ""
	owner, err := s.userRepo.QueryByAppIDOwnership(ctx, exec, appID)
	if err != nil {
		return inviterName, fmt.Errorf("error finding owner for app %s: %w", appID, err)
	}
	if owner != nil {
		inviterName = strings.TrimSpace(fmt.Sprintf("%s %s", owner.FirstName.String, owner.LastName.String))
	}
	return inviterName, nil
}

func orgToOrganizationData(org *model.Organization) templates.OrganizationData {
	return templates.NewOrganizationData(org.ID,
		org.Name,
		json.RawMessage(org.PublicMetadata))
}

type EmailPasskey struct {
	PasskeyName         string
	GreetingName        string
	PrimaryEmailAddress string
}

func (s *Service) SendPasskeyEmail(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	params EmailPasskey,
	slug string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTEmail, slug)
	if err != nil {
		return err
	}

	commonEmailData, err := s.templateSvc.GetCommonEmailData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendPasskeyEmail: populating common email data (instance_id=%s, slug=%s): %w",
			env.Instance.ID, slug, err)
	}

	data := templates.PasskeyEmailData{
		PasskeyName:         params.PasskeyName,
		CommonEmailData:     commonEmailData,
		GreetingName:        params.GreetingName,
		PrimaryEmailAddress: params.PrimaryEmailAddress,
	}

	fromEmailName := s.templateSvc.FromEmailName(template, env.Instance)
	emailData, err := templates.RenderEmail(ctx, data, template, fromEmailName, nil, &params.PrimaryEmailAddress)
	if err != nil {
		return err
	}

	_, err = s.emailService.Send(ctx, tx, emailData, env)
	if err != nil {
		return fmt.Errorf("sendPasskeyEmail: sending email data %+v (slug=%s): %w",
			emailData, slug, err)
	}

	return nil
}

func (s *Service) SendPasskeySMS(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	phoneNumber string,
	slug string,
) error {
	template, err := s.templateSvc.GetTemplate(ctx, tx, env.Instance.ID, constants.TTSMS, slug)
	if err != nil {
		return err
	}

	commonSMSData, err := s.templateSvc.GetCommonSMSData(ctx, env)
	if err != nil {
		return fmt.Errorf("sendPasskeySMS: populating common sms data (instance_id=%s, slug=%s): %w",
			env.Instance.ID, slug, err)
	}

	smsData, err := templates.RenderSMS(commonSMSData, template, nil, &phoneNumber)
	if err != nil {
		return err
	}

	_, err = s.smsService.Send(ctx, tx, smsData, env)
	if err != nil {
		return fmt.Errorf("sendPasskeySMS: sending sms data %+v (slug=%s): %w",
			smsData, slug, err)
	}

	return nil
}
