package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/pkg/constants"
	"clerk/pkg/ctx/trace"

	sentry "github.com/getsentry/sentry-go/http"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
)

func TestSetTraceID_googleCloudToken(t *testing.T) {
	t.Parallel()
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := trace.FromContext(r.Context())
		_, _ = w.Write([]byte(traceID))
	})

	handlerToTest := sentry.New(sentry.Options{Repanic: true}).Handle(SetTraceID(fn))
	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)

	expectedTraceID := "testingTraceID"
	req.Header.Set(googleCloudTraceHeader, expectedTraceID+"/otherData")

	responseRecorder := httptest.NewRecorder()
	handlerToTest.ServeHTTP(responseRecorder, req)

	actualTraceID := responseRecorder.Body.String()
	assert.Equal(t, expectedTraceID, actualTraceID)
}

func TestSetTraceID_chiToken(t *testing.T) {
	t.Parallel()
	var expectedTraceID string
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		expectedTraceID = chimw.GetReqID(ctx)
		traceID := trace.FromContext(ctx)
		_, _ = w.Write([]byte(traceID))
	})

	handlerToTest := sentry.New(sentry.Options{Repanic: true}).Handle(SetTraceID(fn))

	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)

	responseRecorder := httptest.NewRecorder()
	handlerToTest.ServeHTTP(responseRecorder, req)

	actualTraceID := responseRecorder.Body.String()
	assert.NotEmpty(t, actualTraceID)
	assert.Equal(t, expectedTraceID, actualTraceID)
}

func TestSetTraceID_clerkTraceID(t *testing.T) {
	t.Parallel()
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := trace.FromContext(r.Context())
		_, _ = w.Write([]byte(traceID))
	})

	handlerToTest := sentry.New(sentry.Options{Repanic: true}).Handle(SetTraceID(fn))
	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)

	expectedTraceID := "testingTraceID"
	req.Header.Set(constants.ClerkTraceID, expectedTraceID)

	responseRecorder := httptest.NewRecorder()
	handlerToTest.ServeHTTP(responseRecorder, req)

	actualTraceID := responseRecorder.Body.String()
	assert.Equal(t, expectedTraceID, actualTraceID)
}
