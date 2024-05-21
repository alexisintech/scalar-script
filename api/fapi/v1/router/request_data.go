package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/ctx/request_info"
	"clerk/utils/log"
)

func setRequestInfo(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	// set by Cloudflare
	clientIP := r.Header.Get("True-Client-IP")
	if clientIP == "" {
		// set by our Cloudflare Worker and clerk_docker's nginx (local)
		clientIP = r.Header.Get("X-Client-IP")
	}

	requestInfo := model.RequestInfo{
		UserAgent:  r.Header.Get("User-Agent"),
		RemoteAddr: r.RemoteAddr,
		ClientIP:   clientIP,
		Origin:     r.Header.Get("Origin"),
		CFRay:      r.Header.Get("X-Visitor-CF-Ray"),
	}
	newCtx := request_info.NewContext(ctx, &requestInfo)
	log.AddToLogLine(ctx, log.RequestInfo, &requestInfo)

	return r.WithContext(newCtx), nil
}
