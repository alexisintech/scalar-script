package resource

import (
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"

	"github.com/stretchr/testify/assert"
)

func TestFapiEnvironment_CacheTag(t *testing.T) {
	t.Parallel()

	domain := model.Domain{Domain: &sqbmodel.Domain{Name: "example.com"}}
	resource := NewFapiEnvironment(&domain)

	assert.Equal(t, "clerk.example.com:/v1/environment", resource.CacheTag())
}
