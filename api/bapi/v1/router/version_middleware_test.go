package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/pkg/clerkhttp"
	"clerk/utils/log"

	"github.com/stretchr/testify/assert"
)

func TestLogClerkSDKVersion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		expected string
		headers  map[string]string
	}{
		{
			name:     "header exists",
			expected: "v1.0.1",
			headers:  map[string]string{clerkSDKVersionHeader: "v1.0.1"},
		},
		{
			name:     "header doesn't exist, fallback to no_version",
			expected: "no_version",
			headers:  nil,
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var actualVersion string
			fn := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				logLine, _ := log.GetLogLine(r.Context())
				actualVersion = logLine.ValueFromContext(log.ClerkSDKVersion).(string)
			})

			handlerToTest := clerkhttp.Middleware(logClerkSDKVersion)

			req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
			for key, val := range tt.headers {
				req.Header.Set(key, val)
			}

			logLine := log.NewCanonicalLine(req, &loggableResponseRecorder{ResponseRecorder: httptest.NewRecorder()})
			ctx := log.AddLogLineToContext(context.Background(), logLine)
			req = req.WithContext(ctx)

			handlerToTest(fn).ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, tt.expected, actualVersion)
		})
	}
}

type loggableResponseRecorder struct {
	*httptest.ResponseRecorder
}

func (l *loggableResponseRecorder) ResponseSize() int64 {
	return int64(l.ResponseRecorder.Body.Len())
}

func (l *loggableResponseRecorder) ResponseBytes() []byte {
	return l.ResponseRecorder.Body.Bytes()
}

func (l *loggableResponseRecorder) StatusCode() int {
	return l.ResponseRecorder.Code
}
