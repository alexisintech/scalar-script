package router

import (
	"net/http"

	"clerk/pkg/set"

	"github.com/go-chi/cors"
)

func corsHandler(authorizedParties []string) func(http.Handler) http.Handler {
	authorizedPartiesSet := set.New[string](authorizedParties...)
	c := cors.New(cors.Options{
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return authorizedPartiesSet.Contains(origin)
		},
		AllowCredentials: false,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Accept", "Content-Type"},
		MaxAge:           300,
	})
	return c.Handler
}
