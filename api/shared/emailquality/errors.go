package emailquality

type APIError struct {
	Err error
}

func (e APIError) Error() string {
	return "emailquality: " + e.Err.Error()
}

func (e APIError) Unwrap() error {
	return e.Err
}
