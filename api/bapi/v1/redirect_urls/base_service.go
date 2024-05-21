package redirect_urls

import (
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"

	"github.com/go-playground/validator/v10"
)

type Service struct {
	db        database.Database
	clock     clockwork.Clock
	validator *validator.Validate

	redirectUrlsRepo *repository.RedirectUrls
	usersRepo        *repository.Users
}

func NewService(db database.Database, clock clockwork.Clock) *Service {
	return &Service{
		db:               db,
		clock:            clock,
		validator:        validator.New(),
		redirectUrlsRepo: repository.NewRedirectUrls(),
		usersRepo:        repository.NewUsers(),
	}
}
