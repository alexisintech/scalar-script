package domains

import (
	"fmt"
	"testing"

	"clerk/api/shared/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/strings"

	"github.com/stretchr/testify/assert"
	"github.com/volatiletech/null/v8"
)

func TestDomainsServiceMailStatus(t *testing.T) {
	t.Parallel()
	testCases := map[struct {
		SendgridJobInflight bool
		SendgridResponse    *string
		DomainVerified      bool
	}]string{
		{SendgridJobInflight: false, SendgridResponse: nil, DomainVerified: false}:                           constants.MAILNotStarted,
		{SendgridJobInflight: true, SendgridResponse: nil, DomainVerified: false}:                            constants.MAILInProgress,
		{SendgridJobInflight: true, SendgridResponse: strings.ToPtr("some-response"), DomainVerified: false}: constants.MAILFailed,
		{SendgridJobInflight: true, SendgridResponse: strings.ToPtr("some-response"), DomainVerified: true}:  constants.MAILComplete,
	}

	for input, expected := range testCases {
		input, expected := input, expected
		t.Run(fmt.Sprintf("%#v -> %s", input, expected), func(t *testing.T) {
			t.Parallel()
			domain := &model.Domain{Domain: &sqbmodel.Domain{}}
			instance := &model.Instance{Instance: &sqbmodel.Instance{EnvironmentType: string(constants.ETProduction)}}
			domain.SendgridDomainVerified = input.DomainVerified
			domain.SendgridJobInflight = input.SendgridJobInflight
			if input.SendgridResponse != nil {
				domain.SendgridDomainVerifiedResponse = null.JSONFrom([]byte(*input.SendgridResponse))
			}

			mailStatus := getMailStatus(domain, instance)
			assert.Equal(t, serialize.MailStatus(expected), mailStatus)
		})
	}
}
