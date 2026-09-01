package llm

import "errors"

// Some failures are a verdict about the situation, not about the request: the
// box is not reachable, the plan is not signed in, the plan is spent, the boss
// stopped it. Trying again produces the same sentence a second time and makes
// him wait twice to read it.
//
// Everything else is worth one more attempt, so the marking goes on the few
// that are hopeless rather than on the many that are not. A caller that
// forgets to mark one costs a retry; a caller that marked the wrong thing
// costs the boss an answer, which is why the default is to retry.

type unrecoverable struct{ err error }

func (u unrecoverable) Error() string { return u.err.Error() }
func (u unrecoverable) Unwrap() error { return u.err }

// Unrecoverable marks an error as one a second attempt cannot fix. The
// message is untouched, so what the boss reads does not change.
func Unrecoverable(err error) error {
	if err == nil {
		return nil
	}
	return unrecoverable{err}
}

// IsUnrecoverable reports whether err was marked by Unrecoverable.
func IsUnrecoverable(err error) bool {
	var u unrecoverable
	return errors.As(err, &u)
}
