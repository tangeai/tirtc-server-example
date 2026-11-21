// Package backoff supplies jitter only; the caller owns retry eligibility,
// attempt limits and cancellation. It must not be used to retry uncertain writes.
package backoff

import (
	"math/rand"
	"time"
)

// Jitter returns an equal-jitter delay in [ceiling/2, ceiling].
func Jitter(ceiling time.Duration) time.Duration {
	if ceiling <= 1 {
		return max(0, ceiling)
	}
	half := ceiling / 2
	return half + time.Duration(rand.Int63n(int64(ceiling-half)+1))
}
