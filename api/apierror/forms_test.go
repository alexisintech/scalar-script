package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"clerk/pkg/clerkvalidator"
	"clerk/pkg/set"

	"github.com/go-playground/validator/v10"
)

func TestFormValidationFailed(t *testing.T) {
	t.Parallel()
	validate := clerkvalidator.New()

	type User struct {
		Username string `json:"username" validate:"required"`
		Email    string `json:"email" validate:"required,email"`
	}

	testCases := map[User]struct {
		NumberOfErrors int
		HTTPCode       int
		ParamsToFail   set.Set[string]
	}{
		{Username: "un", Email: "email@domain.com"}: {NumberOfErrors: 0},

		{Email: "email@domain.com"}:              {1, http.StatusUnprocessableEntity, set.New("username")},
		{Username: "un"}:                         {1, http.StatusUnprocessableEntity, set.New("email")},
		{Username: "un", Email: "invalid_email"}: {1, http.StatusUnprocessableEntity, set.New("email")},
		{}:                                       {2, http.StatusUnprocessableEntity, set.New("username", "email")},
	}

	for input, expected := range testCases {
		input, expected := input, expected
		t.Run(fmt.Sprintf("%#v", input), func(t *testing.T) {
			t.Parallel()

			err := validate.Struct(input)
			var validationErr validator.ValidationErrors
			errors.As(err, &validationErr)

			if validationErr == nil {
				if expected.NumberOfErrors > 0 {
					t.Fatalf("expected %d errors, found no errors instead", expected.NumberOfErrors)
				}
				return
			}

			actual := FormValidationFailed(validationErr)

			if actual.HTTPCode() != expected.HTTPCode {
				t.Errorf("expected %d HTTP code, found %d instead", expected.HTTPCode, actual.HTTPCode())
			}
			if len(actual.Errors()) != expected.NumberOfErrors {
				t.Errorf("expected %d number of errors, found %d instead: %#v", expected.NumberOfErrors, len(actual.Errors()), actual.Errors())
			}
			for _, err := range actual.Errors() {
				actualFailedParam := err.Meta().(*formParameter).Name
				if !expected.ParamsToFail.Contains(actualFailedParam) {
					t.Errorf("found unexpected failed param %s", actualFailedParam)
				} else {
					expected.ParamsToFail.Remove(actualFailedParam)
				}
			}
			if !expected.ParamsToFail.IsEmpty() {
				t.Errorf("expected following params to fail: %v", expected.ParamsToFail.Array())
			}
		})
	}
}
