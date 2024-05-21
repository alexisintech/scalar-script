package internalapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"clerk/api/bapi/v1/internalapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWhatIsMyIP(t *testing.T) {
	t.Parallel()
	want := "1.1.1.1"
	authorization := "open-sesame"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, authorization, r.Header.Get("authorization"))
		fmt.Fprintf(w, `{"ip":"%s"}`, want)
	}))
	client := internalapi.NewClient(ts.URL, ts.Client())
	res, err := client.WhatIsMyIP(context.Background(), authorization)
	require.NoError(t, err)
	assert.Equal(t, want, res.IP)
}

func TestWhatIsMyIP_InvalidBaseURL(t *testing.T) {
	t.Parallel()
	client := internalapi.NewClient("wrong", nil)
	_, err := client.WhatIsMyIP(context.Background(), "authorization")
	require.Error(t, err)
}

func TestWhatIsMyIP_InvalidJSON(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "notjson")
	}))
	client := internalapi.NewClient(ts.URL, ts.Client())
	_, err := client.WhatIsMyIP(context.Background(), "authorization")
	require.Error(t, err)
}
