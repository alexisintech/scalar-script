package user_profile

import (
	"fmt"
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/null/v8"
)

func TestGetImageURLWithoutNameAttributes(t *testing.T) {
	t.Parallel()

	clock := clockwork.NewFakeClock()
	service := NewService(clock)
	user := &model.User{
		User: &sqbmodel.User{
			ID:         "user_xxxxxxxxx",
			InstanceID: "ins_xxxxxxxxx",
		},
	}

	imageURL, err := service.GetImageURL(user)

	encodedImageIdentifier := "eyJ0eXBlIjoiZGVmYXVsdCIsImlpZCI6Imluc194eHh4eHh4eHgiLCJyaWQiOiJ1c2VyX3h4eHh4eHh4eCJ9"
	expectedImageURL := fmt.Sprintf("%s/%s", cenv.Get(cenv.ClerkImageServiceURL), encodedImageIdentifier)

	require.NoError(t, err)
	assert.Equal(t, expectedImageURL, imageURL)
}

func TestGetImageURLWithOnlyFirstName(t *testing.T) {
	t.Parallel()

	clock := clockwork.NewFakeClock()
	service := NewService(clock)
	user := &model.User{
		User: &sqbmodel.User{
			ID:         "user_xxxxxxxxx",
			InstanceID: "ins_xxxxxxxxx",
			FirstName:  null.StringFrom("first_name"),
		},
	}

	imageURL, err := service.GetImageURL(user)

	encodedImageIdentifier := "eyJ0eXBlIjoiZGVmYXVsdCIsImlpZCI6Imluc194eHh4eHh4eHgiLCJyaWQiOiJ1c2VyX3h4eHh4eHh4eCIsImluaXRpYWxzIjoiRiJ9"
	expectedImageURL := fmt.Sprintf("%s/%s", cenv.Get(cenv.ClerkImageServiceURL), encodedImageIdentifier)

	require.NoError(t, err)
	assert.Equal(t, expectedImageURL, imageURL)
}

func TestGetImageURLWithOnlyLastName(t *testing.T) {
	t.Parallel()

	clock := clockwork.NewFakeClock()
	service := NewService(clock)
	user := &model.User{
		User: &sqbmodel.User{
			ID:         "user_xxxxxxxxx",
			InstanceID: "ins_xxxxxxxxx",
			LastName:   null.StringFrom("last_name"),
		},
	}

	imageURL, err := service.GetImageURL(user)

	encodedImageIdentifier := "eyJ0eXBlIjoiZGVmYXVsdCIsImlpZCI6Imluc194eHh4eHh4eHgiLCJyaWQiOiJ1c2VyX3h4eHh4eHh4eCIsImluaXRpYWxzIjoiTCJ9"
	expectedImageURL := fmt.Sprintf("%s/%s", cenv.Get(cenv.ClerkImageServiceURL), encodedImageIdentifier)

	require.NoError(t, err)
	assert.Equal(t, expectedImageURL, imageURL)
}

func TestGetImageURLWithFullName(t *testing.T) {
	t.Parallel()

	clock := clockwork.NewFakeClock()
	service := NewService(clock)
	user := &model.User{
		User: &sqbmodel.User{
			ID:         "user_xxxxxxxxx",
			InstanceID: "ins_xxxxxxxxx",
			FirstName:  null.StringFrom("first_name"),
			LastName:   null.StringFrom("last_name"),
		},
	}

	imageURL, err := service.GetImageURL(user)

	encodedImageIdentifier := "eyJ0eXBlIjoiZGVmYXVsdCIsImlpZCI6Imluc194eHh4eHh4eHgiLCJyaWQiOiJ1c2VyX3h4eHh4eHh4eCIsImluaXRpYWxzIjoiRkwifQ"
	expectedImageURL := fmt.Sprintf("%s/%s", cenv.Get(cenv.ClerkImageServiceURL), encodedImageIdentifier)

	require.NoError(t, err)
	assert.Equal(t, expectedImageURL, imageURL)
}

func TestGetImageURLWithFullNameAndProfileImagePublicURL(t *testing.T) {
	t.Parallel()

	profileImageURL := "http://example.com/image_url"
	clock := clockwork.NewFakeClock()
	service := NewService(clock)
	user := &model.User{
		User: &sqbmodel.User{
			ID:                    "user_xxxxxxxxx",
			InstanceID:            "ins_xxxxxxxxx",
			FirstName:             null.StringFrom("first_name"),
			LastName:              null.StringFrom("last_name"),
			ProfileImagePublicURL: null.StringFrom(profileImageURL),
		},
	}

	imageURL, err := service.GetImageURL(user)

	encodedImageIdentifier := "eyJ0eXBlIjoicHJveHkiLCJzcmMiOiJodHRwOi8vZXhhbXBsZS5jb20vaW1hZ2VfdXJsIiwicyI6IkZJT3k3NXI4ZXpvd3BlQitoU0dxSmNUK2djMW8xd211TjZXcGIvNXlwMFkifQ"
	expectedImageURL := fmt.Sprintf("%s/%s", cenv.Get(cenv.ClerkImageServiceURL), encodedImageIdentifier)

	require.NoError(t, err)
	assert.Equal(t, expectedImageURL, imageURL)
}
