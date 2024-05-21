package strategies

import clerkurl "clerk/utils/url"

func resolveRelativeURL(origin, inputURL string) (string, error) {
	isRelative, err := clerkurl.IsRelative(inputURL)
	if err != nil {
		return "", ErrInvalidRedirectURL
	}
	if isRelative && origin != "" {
		return origin + inputURL, nil
	}
	return inputURL, nil
}
