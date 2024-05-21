package serialize_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

func TestSessionToServerAPI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		orgID     null.String
		isPresent bool
	}{
		{null.StringFrom("org_id"), true},
		{null.StringFromPtr(nil), false},
	} {
		session := &model.Session{
			Session: &sqbmodel.Session{ActiveOrganizationID: tc.orgID},
		}
		serialized := serialize.SessionToServerAPI(clockwork.NewFakeClock(), session)
		raw, err := json.Marshal(serialized)
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]interface{})
		err = json.Unmarshal(raw, &out)
		if err != nil {
			t.Fatal(err)
		}
		lastActiveOrganizationID, ok := out["last_active_organization_id"]
		if ok != tc.isPresent {
			t.Errorf("expected key presence: %v", tc.isPresent)
		} else if ok {
			got := fmt.Sprintf("%s", lastActiveOrganizationID)
			if got != tc.orgID.String {
				t.Errorf("want: %s,\n got: %s", tc.orgID.String, got)
			}
		}
	}
}
