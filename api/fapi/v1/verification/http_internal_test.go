package verification

import (
	"fmt"
	"testing"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/utils/param"
)

func TestBuildVerifyTokenRedirectURLQueryString(t *testing.T) {
	t.Parallel()

	for i, tc := range []struct {
		base string
		want string
	}{
		{"https://example.com/?foo=bar", "https://example.com/?__clerk_created_session=session_id&__clerk_status=verified&foo=bar"},
		{"https://example.com?foo=bar#/", "https://example.com?__clerk_created_session=session_id&__clerk_status=verified&foo=bar#/"},
		{"https://example.com/whatever?foo=bar", "https://example.com/whatever?__clerk_created_session=session_id&__clerk_status=verified&foo=bar"},
		{"https://example.com?foo=bar#/whatever", "https://example.com?__clerk_created_session=session_id&__clerk_status=verified&foo=bar#/whatever"},
		{"https://example.com/whatever", "https://example.com/whatever?__clerk_created_session=session_id&__clerk_status=verified"},
		{"https://example.com#/whatever", "https://example.com?__clerk_created_session=session_id&__clerk_status=verified#/whatever"},
		{"https://example.com", "https://example.com?__clerk_created_session=session_id&__clerk_status=verified"},
		{"https://example.com#/", "https://example.com?__clerk_created_session=session_id&__clerk_status=verified#/"},
	} {
		createdSession := &model.Session{Session: &sqbmodel.Session{ID: "session_id"}}
		u, err := buildVerifyTokenRedirectURL(tc.base, createdSession, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.want != u.String() {
			t.Errorf("(%d) want: %s, got %s", i, tc.want, u.String())
		}
	}
}

func TestBuildVerifyTokenRedirectURLStatus(t *testing.T) {
	t.Parallel()
	for i, tc := range []struct {
		err  apierror.Error
		want string
	}{
		{apierror.VerificationLinkTokenExpired(), "expired"},
		{apierror.VerificationInvalidLinkToken(), "failed"},
		{apierror.Unexpected(fmt.Errorf("an-error")), "failed"},
		{nil, "verified"},
	} {
		u, err := buildVerifyTokenRedirectURL("https://example.com", nil, tc.err)
		if err != nil {
			t.Fatal(err)
		}
		got := u.Query().Get(param.ClerkStatus)
		if tc.want != got {
			t.Errorf("(%d) want: %s, got %s", i, tc.want, got)
		}
	}
}
