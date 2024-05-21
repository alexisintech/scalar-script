package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/utils/log"
)

func TestLogMiddleware(t *testing.T) {
	t.Parallel()
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, logLineExists := log.GetLogLine(r.Context())
		if !logLineExists {
			t.Error("Log line is missing")
		}
	})

	logger := log.New()
	ctx := log.NewContextWithLogger(context.Background(), logger)
	handlerToTest := Log(func() sql.DBStats {
		return sql.DBStats{}
	})(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "http://testing", nil)
	req = req.WithContext(ctx)

	handlerToTest.ServeHTTP(httptest.NewRecorder(), req)
}
