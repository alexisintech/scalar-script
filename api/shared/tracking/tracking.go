package tracking

import (
	"net/http"
	"strings"

	"clerk/pkg/constants"
)

type GoogleAnalyticsCookie struct {
	Name  string
	Value string
}

type Data struct {
	GoogleAnalyticsCookies []*GoogleAnalyticsCookie
}

func NewDataFromRequest(r *http.Request) *Data {
	data := &Data{}

	for _, cookie := range r.Cookies() {
		if strings.HasPrefix(cookie.Name, constants.GoogleAnalytics) {
			data.GoogleAnalyticsCookies = append(data.GoogleAnalyticsCookies, &GoogleAnalyticsCookie{Name: cookie.Name, Value: cookie.Value})
		}
	}

	return data
}

// GAClientID extracts the GA client id from the corresponding GA cookie.
// Google Analytics client ID cookie values have the following format:
// GAX.Y.WWW.ZZZ
// The client_id part is: WWW.ZZZ
func (d *Data) GAClientID() string {
	cookie := d.gaClientCookie()

	if cookie == nil {
		return ""
	}

	parts := strings.Split(cookie.Value, ".")

	if len(parts) != 4 {
		return ""
	}

	return strings.Join(parts[2:], ".")
}

// GASessionID extracts the GA session id from the corresponding GA cookie.
// Google Analytics sessions ID cookie values have the following format:
// GS1.1.<session_id>.1.0.<timestamp>.0.0.0
func (d *Data) GASessionID(measurementID string) string {
	cookie := d.gaSessionCookie(measurementID)

	if cookie == nil {
		return ""
	}

	parts := strings.Split(cookie.Value, ".")

	if len(parts) != 9 {
		return ""
	}

	return parts[2]
}

// gaClientCookie returns the cookie with name _ga
func (d *Data) gaClientCookie() *GoogleAnalyticsCookie {
	for _, cookie := range d.GoogleAnalyticsCookies {
		if cookie.Name == constants.GoogleAnalytics {
			return cookie
		}
	}

	return nil
}

// gaSessionCookie returns the cookie with name _ga_{measurementID}
func (d *Data) gaSessionCookie(measurementID string) *GoogleAnalyticsCookie {
	for _, cookie := range d.GoogleAnalyticsCookies {
		if cookie.Name == constants.GoogleAnalytics+"_"+measurementID {
			return cookie
		}
	}

	return nil
}
