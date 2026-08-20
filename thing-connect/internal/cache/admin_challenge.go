package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const adminMFAChallengePrefix = "thingconnect:admin:mfa-challenge:"

// AdminMFAChallengeStore is the Redis adapter for admin.MFAChallengeStore.
// Keeping the Redis key and atomic SET NX behavior here prevents transport
// handlers from owning authentication state transitions.
type AdminMFAChallengeStore struct {
	client *redis.Client
}

func NewAdminMFAChallengeStore(client *redis.Client) *AdminMFAChallengeStore {
	return &AdminMFAChallengeStore{client: client}
}

func (s *AdminMFAChallengeStore) Claim(ctx context.Context, challengeID string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil || challengeID == "" || ttl <= 0 {
		return false, errors.New("invalid MFA challenge claim")
	}
	return s.client.SetNX(ctx, adminMFAChallengePrefix+challengeID, "1", ttl).Result()
}

func (s *AdminMFAChallengeStore) Release(ctx context.Context, challengeID string) error {
	if s == nil || s.client == nil || challengeID == "" {
		return errors.New("invalid MFA challenge release")
	}
	return s.client.Del(ctx, adminMFAChallengePrefix+challengeID).Err()
}
