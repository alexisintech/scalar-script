package pricing

import (
	"net/url"
	"strings"
	"testing"
)

var dashboardOrigin = "https://example.com"

func urlFromString(t *testing.T, strURL string) *url.URL {
	t.Helper()

	u, err := url.Parse(strURL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func assertPath(t *testing.T, urlString, path string) {
	t.Helper()

	u, err := url.Parse(urlString)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != path {
		t.Errorf("Expected %s to have path %s", u.String(), u.Path)
	}
}

func assertQueryParam(t *testing.T, urlString, key, value string) {
	t.Helper()

	u, err := url.Parse(urlString)
	if err != nil {
		t.Fatal(err)
	}
	vals := u.Query()
	if len(vals[key]) != 1 || vals.Get(key) != value {
		t.Errorf("expected query string %s to have param %s once with value %s", u.Query().Encode(), key, value)
	}
}

func assertQueryParamMissing(t *testing.T, urlString, key string) {
	t.Helper()

	u, err := url.Parse(urlString)
	if err != nil {
		t.Fatal(err)
	}
	vals := u.Query()
	_, exists := vals[key]
	if exists { // go 1.17: vals.Has(key)
		t.Errorf("expected query string %s not to have param %s", u.Query().Encode(), key)
	}
}

func TestDashboardV1ReturnURL(t *testing.T) {
	t.Parallel()
	u0 := dashboardReturnURL("app_1", "sess_2", dashboardOrigin, urlFromString(t, ""))
	u := dashboardV1ReturnURL("app_1", "sess_2", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	exp := dashboardOrigin + "/applications/app_1/billing"
	if !strings.HasPrefix(u, exp) {
		t.Errorf("expected %s to have prefix %s", u, exp)
	}
	if !strings.Contains(u, "clerk_session_id=sess_2") {
		t.Errorf("expected %s to contain clerk_session_id=sess_2", u)
	}
}

func TestDashboardV1SuccessURL(t *testing.T) {
	t.Parallel()
	u0 := dashboardSuccessURL("app_2", "sess_4", dashboardOrigin, urlFromString(t, ""))
	u := dashboardV1SuccessURL("app_2", "sess_4", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	exp := dashboardOrigin + "/applications/app_2/billing"
	if !strings.HasPrefix(u, exp) {
		t.Errorf("expected %s to have prefix %s", u, exp)
	}
	if !strings.Contains(u, "clerk_session_id=sess_4") {
		t.Errorf("expected %s to contain clerk_session_id=sess_4", u)
	}
}

func TestDashboardV1CancelURL(t *testing.T) {
	t.Parallel()
	u0 := dashboardCancelURL("app_3", "sess_6", dashboardOrigin, urlFromString(t, ""))
	u := dashboardV1CancelURL("app_3", "sess_6", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	exp := dashboardOrigin + "/applications/app_3/billing"
	if !strings.HasPrefix(u, exp) {
		t.Errorf("expected %s to have prefix %s", u, exp)
	}
	if !strings.Contains(u, "clerk_session_id=sess_6") {
		t.Errorf("expected %s to contain clerk_session_id=sess_6", u)
	}
}

func TestDashboardV2ReturnURL(t *testing.T) {
	t.Parallel()
	retURL := urlFromString(t, "/v2/app_x/ins_y/b?tab=pay&checkout=success")
	u0 := dashboardReturnURL("x", "sess_z", dashboardOrigin, retURL)
	u := dashboardV2ReturnURL(retURL, "sess_z", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	assertPath(t, u, "/v2/app_x/ins_y/b")
	assertQueryParam(t, u, "tab", "pay")
	assertQueryParam(t, u, "clerk_session_id", "sess_z")
	assertQueryParamMissing(t, u, "checkout")
}

func TestDashboardV2SuccessURL(t *testing.T) {
	t.Parallel()
	retURL := urlFromString(t, "/v2/app_1/ins_2/b?tab=pay&checkout=canceled")
	u0 := dashboardSuccessURL("x", "sess_q", dashboardOrigin, retURL)
	u := dashboardV2SuccessURL(retURL, "sess_q", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	assertPath(t, u, "/v2/app_1/ins_2/b")
	assertQueryParam(t, u, "tab", "pay")
	assertQueryParam(t, u, "checkout", "success")
	assertQueryParam(t, u, "clerk_session_id", "sess_q")
	assertQueryParam(t, u, "checkout_session_id", "{CHECKOUT_SESSION_ID}")
}

func TestDashboardV2CancelURL(t *testing.T) {
	t.Parallel()
	retURL := urlFromString(t, "/v2/app_3/ins_4/b?tab=billing&checkout_session_id=cs_test_x")
	u0 := dashboardCancelURL("x", "sess_w", dashboardOrigin, retURL)
	u := dashboardV2CancelURL(retURL, "sess_w", dashboardOrigin)
	if u0 != u {
		t.Errorf("expected %s to equal %s", u0, u)
	}

	assertPath(t, u, "/v2/app_3/ins_4/b")
	assertQueryParam(t, u, "tab", "billing")
	assertQueryParam(t, u, "checkout", "canceled")
	assertQueryParam(t, u, "clerk_session_id", "sess_w")
	assertQueryParam(t, u, "checkout_session_id", "{CHECKOUT_SESSION_ID}")
}
