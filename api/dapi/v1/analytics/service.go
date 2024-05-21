package analytics

import (
	"context"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	db    database.Database
	clock clockwork.Clock

	// services
	userProfileService *user_profile.Service

	// repositories
	dailyAggregationRepo      *repository.DailyAggregations
	dailyUniqueActiveUsers    *repository.DailyUniqueActiveUsers
	dailySuccessfulSignInRepo *repository.DailySuccessfulSignIns
	dailySuccessfulSignUpRepo *repository.DailySuccessfulSignUps
	userRepo                  *repository.Users
	signinRepo                *repository.SignIn
	signupRepo                *repository.SignUp
}

func NewService(clock clockwork.Clock, db database.Database) *Service {
	return &Service{
		db:                        db,
		clock:                     clock,
		userProfileService:        user_profile.NewService(clock),
		dailyAggregationRepo:      repository.NewDailyAggregations(),
		dailyUniqueActiveUsers:    repository.NewDailyUniqueActiveUsers(),
		dailySuccessfulSignInRepo: repository.NewDailySuccessfulSignIns(),
		dailySuccessfulSignUpRepo: repository.NewDailySuccessfulSignUps(),
		userRepo:                  repository.NewUsers(),
		signinRepo:                repository.NewSignIn(),
		signupRepo:                repository.NewSignUp(),
	}
}

// ActiveUsers returns the number of active users per day/week etc. since the
// given day.
func (s *Service) ActiveUsers(
	ctx context.Context,
	instanceID string,
	since time.Time,
	until time.Time,
	interval string,
) (*repository.AnalyticsDataPoints, apierror.Error) {
	points, err := s.dailyUniqueActiveUsers.QueryByInstanceRangeAndInterval(ctx, s.db, instanceID, since, until, interval)
	if err != nil {
		return &points, apierror.Unexpected(err)
	}

	return &points, nil
}

// Signups returns the number of signups per day/week etc. since the given day.
func (s *Service) Signups(
	ctx context.Context,
	instanceID string,
	since time.Time,
	until time.Time,
	interval string,
) (*repository.AnalyticsDataPoints, apierror.Error) {
	points, err := s.dailySuccessfulSignUpRepo.QueryByInstanceRangeAndInterval(ctx,
		s.db, instanceID, since, until, interval)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return &points, nil
}

// Signins returns the number of signins per day/week etc. since the given day.
func (s *Service) Signins(
	ctx context.Context,
	instanceID string,
	since time.Time,
	until time.Time,
	interval string,
) (*repository.AnalyticsDataPoints, apierror.Error) {
	points, err := s.dailySuccessfulSignInRepo.QueryByInstanceRangeAndInterval(ctx,
		s.db, instanceID, since, until, interval)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return &points, nil
}

type MonthlyMetrics struct {
	Year        int        `json:"year"`
	Month       time.Month `json:"month"`
	ActiveUsers int64      `json:"active_users"`
	Signups     int64      `json:"signups"`
	Signins     int64      `json:"signins"`
	TotalUsers  int64      `json:"total_users"`
}

// MonthlyMetrics returns totals such as sign-ins, sign-ups, active users etc.
// for the given year and month and the one before
func (s *Service) MonthlyMetrics(
	ctx context.Context,
	instanceID string,
	year int,
	month time.Month,
) (MonthlyMetrics, apierror.Error) {
	metrics := MonthlyMetrics{Year: year, Month: month}
	var err error

	metrics.TotalUsers, err = s.userRepo.CountForInstance(ctx, s.db, instanceID)
	if err != nil {
		return metrics, apierror.Unexpected(err)
	}

	currentStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	currentEnd := time.Now()

	metrics.Signins, err = s.dailySuccessfulSignInRepo.CountForInstanceAndRange(
		ctx, s.db, instanceID, currentStart, currentEnd)
	if err != nil {
		return metrics, apierror.Unexpected(err)
	}

	metrics.Signups, err = s.dailySuccessfulSignUpRepo.CountForInstanceAndRange(
		ctx, s.db, instanceID, currentStart, currentEnd)
	if err != nil {
		return metrics, apierror.Unexpected(err)
	}

	metrics.ActiveUsers, err = s.dailyUniqueActiveUsers.CountForInstanceAndRange(
		ctx, s.db, instanceID, currentStart, currentEnd)
	if err != nil {
		return metrics, apierror.Unexpected(err)
	}

	return metrics, nil
}

type ActivityItem struct {
	FirstName       string    `json:"first_name"`
	LastName        string    `json:"last_name"`
	Identifier      string    `json:"identifier"`
	Time            time.Time `json:"time"`
	UserID          string    `json:"user_id"`
	ProfileImageURL string    `json:"profile_image_url"`
	ImageURL        *string   `json:"image_url,omitempty"`
}

type LatestActivity struct {
	Signups []ActivityItem `json:"signups"`
	Signins []ActivityItem `json:"signins"`
}

// LatestActivity returns the latest sign-ups and sign-ins up to the provided
// limit
func (s *Service) LatestActivity(
	ctx context.Context,
	instanceID string,
	limit int,
) (LatestActivity, apierror.Error) {
	la := LatestActivity{}

	signups, err := s.signupRepo.QueryLatestSuccessfulByInstanceAndLimit(ctx, s.db, instanceID, limit)
	if err != nil {
		return la, apierror.Unexpected(err)
	}
	la.Signups = make([]ActivityItem, len(signups))
	for i := range signups {
		u := model.User{User: signups[i].R.CreatedUser}
		item := ActivityItem{
			FirstName:       u.FirstName.String,
			LastName:        u.LastName.String,
			Identifier:      s.getUserPrimaryIdentifier(ctx, s.db, u, nil),
			Time:            signups[i].CreatedAt,
			UserID:          u.ID,
			ProfileImageURL: u.ProfileImagePublicURL.String,
		}

		imageURL, err := s.userProfileService.GetImageURL(&u)
		// omit ImageURL in case of error
		if err == nil {
			item.ImageURL = &imageURL
		}

		la.Signups[i] = item
	}

	signIns, err := s.signinRepo.QueryLatestSuccessfulByInstanceAndLimit(ctx, s.db, instanceID, limit)
	if err != nil {
		return la, apierror.Unexpected(err)
	}
	la.Signins = make([]ActivityItem, len(signIns))

	for i := range signIns {
		identification := model.Identification{Identification: signIns[i].R.Identification}
		user := model.User{User: identification.R.User}
		item := ActivityItem{
			FirstName:       user.FirstName.String,
			LastName:        user.LastName.String,
			Identifier:      s.getUserPrimaryIdentifier(ctx, s.db, user, &identification),
			Time:            signIns[i].CreatedAt,
			UserID:          user.ID,
			ProfileImageURL: user.ProfileImagePublicURL.String,
		}

		imageURL, err := s.userProfileService.GetImageURL(&user)
		// omit ImageURL in case of error
		if err == nil {
			item.ImageURL = &imageURL
		}

		la.Signins[i] = item
	}
	return la, nil
}

func (s *Service) getUserPrimaryIdentifier(ctx context.Context, exec database.Executor, user model.User, identification *model.Identification) string {
	if identification != nil && identification.IsUserPrimary(&user) {
		return identification.Identifier.String
	}

	email, err := s.userProfileService.GetPrimaryEmailAddress(ctx, exec, &user)
	if email != nil && err == nil {
		return *email
	}

	phone, err := s.userProfileService.GetPrimaryPhoneNumber(ctx, exec, &user)
	if phone != nil && err == nil {
		return *phone
	}

	return ""
}
