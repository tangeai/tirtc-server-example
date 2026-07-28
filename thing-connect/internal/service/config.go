package service

import "time"

// ServiceConfig holds business-rule parameters.
// All fields are optional in config.yaml; zero values get defaults via config.Load.
type ServiceConfig struct {
	QuotaPerUser               int
	CodeTTL                    time.Duration
	RateLimitWindow            time.Duration
	RateLimitMaxHits           int
	IPRateLimitWindow          time.Duration
	IPRateLimitMaxFingerprints int
	GlobalMaxPendingCodes      int
	TokenExpiry                time.Duration
	MQTTACKTimeout             time.Duration
}

// DefaultServiceConfig returns safe production defaults.
func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		QuotaPerUser:              10,
		CodeTTL:                   190 * time.Second,
		RateLimitWindow:           190 * time.Second,
		RateLimitMaxHits:          10,
		IPRateLimitWindow:         60 * time.Second,
		IPRateLimitMaxFingerprints: 50,
		GlobalMaxPendingCodes:     10000,
		TokenExpiry:               7 * 24 * time.Hour,
		MQTTACKTimeout:            5 * time.Second,
	}
}
