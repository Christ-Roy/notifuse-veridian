package emailerror

import "errors"

// PreAcceptanceError marks a provider failure for which the transport knows
// that the remote server did not accept the message. Ambiguous failures after
// the DATA body are intentionally not marked.
type PreAcceptanceError struct{ Err error }

func (e *PreAcceptanceError) Error() string { return e.Err.Error() }
func (e *PreAcceptanceError) Unwrap() error { return e.Err }

func BeforeAcceptance(err error) error {
	if err == nil {
		return nil
	}
	return &PreAcceptanceError{Err: err}
}

func IsBeforeAcceptance(err error) bool {
	var marked *PreAcceptanceError
	return errors.As(err, &marked)
}
