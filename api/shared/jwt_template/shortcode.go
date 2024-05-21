package jwt_template

import "context"

// a shortcode is a special JSON string that may appear as a token claim value
// inside a template. During template execution, it will be substituted for an
// actual value.
//
// Available shortcodes are defined by package shortcodes and are identified by
// by shortcode.Identifier.
//
// Also see documentation of Execute.
type shortcode interface {
	// Identifier uniquely identifies the shortcode and if encountered inside the
	// claims, it will be substituted with the return value of Substitute.
	Identifier() string

	// Substitute returns the value that will substitute the Code.
	Substitute(ctx context.Context) (any, error)
}
