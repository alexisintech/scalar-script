package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestStripV1(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()

	r.Use(StripV1)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nothing here"))
	})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		queryParam := r.URL.Query().Get("id")
		if queryParam != "" {
			_, _ = w.Write([]byte(queryParam))
			return
		}
		_, _ = w.Write([]byte("root"))
	})

	r.Get("/queryParamId", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Query().Get("id")))
	})

	r.Route("/pathToken/{id}", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(chi.URLParam(r, "id")))
		})
	})

	r.Route("/v1pathToken/unusual-endpoint", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	})

	r.Route("/doesntWantNestedV1Stripped", func(r chi.Router) {
		r.Get("/isRoot", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("isRoot"))
		})

		r.Get("/v1/isV1", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("isV1NestedEndpoint"))
		})
	})

	r.Route("/wantsNestedV1Stripped", func(r chi.Router) {
		r.Use(StripV1)
		r.Get("/isV1", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("isV1NestedEndpoint"))
		})
	})

	tests := []struct {
		name     string
		path     string
		wantResp string
		wantCode int
	}{
		{
			name:     "non-v1 at the root",
			path:     "/",
			wantResp: "root",
			wantCode: 200,
		},
		{
			name:     "v1 at the root",
			path:     "/v1",
			wantResp: "root",
			wantCode: 200,
		},

		{
			name:     "non-v1 at the root with query param",
			path:     "/?id=foo",
			wantResp: "foo",
			wantCode: 200,
		},
		{
			name:     "v1 at the root with a query param",
			path:     "/v1?id=foo",
			wantResp: "foo",
			wantCode: 200,
		},

		{
			name:     "non-v1 nested",
			path:     "/pathToken/my-obj",
			wantResp: "my-obj",
			wantCode: 200,
		},
		{
			name:     "v1 nested",
			path:     "/v1/pathToken/my-obj",
			wantResp: "my-obj",
			wantCode: 200,
		},

		{
			name:     "non-v1 query param",
			path:     "/queryParamId?id=foo",
			wantResp: "foo",
			wantCode: 200,
		},
		{
			name:     "non-v1 query param",
			path:     "/v1/queryParamId?id=foo",
			wantResp: "foo",
			wantCode: 200,
		},

		{
			name:     "v1 at the root with a trailing slash",
			path:     "/v1/",
			wantResp: "root",
			wantCode: 200,
		},

		{
			name:     "Doesn't strip routes that start with v1",
			path:     "/v1pathToken/unusual-endpoint",
			wantCode: 418,
		},

		{
			name:     "Doesn't strip routes that have a nested v1 in path",
			path:     "/doesntWantNestedV1Stripped/v1/isV1",
			wantResp: "isV1NestedEndpoint",
			wantCode: 200,
		},

		{
			name:     "Doesn't strip routes that have an extra v1 included in the request path",
			path:     "/doesntWantNestedV1Stripped/v1/isRoot",
			wantCode: 404,
		},

		{
			name:     "Doesn't strip routes that have a nested v1 _unless_ the parent route has StripV1",
			path:     "/wantsNestedV1Stripped/v1/isV1", // Note that "v1" isn't declared in the parent route
			wantResp: "isV1NestedEndpoint",
			wantCode: 200,
		},
	}

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require := require.New(t)

			req, err := http.NewRequest(http.MethodGet, ts.URL+tt.path, nil)
			require.NoError(err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(err)
			require.Equal(tt.wantCode, resp.StatusCode)

			if tt.wantResp != "" {
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(err)
				defer resp.Body.Close()

				require.Equal(tt.wantResp, string(respBody))
			}
		})
	}
}
