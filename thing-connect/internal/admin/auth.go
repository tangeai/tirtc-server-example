package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials   = errors.New("admin: invalid credentials")
	ErrAccountDisabled      = errors.New("admin: account disabled")
	ErrInvalidToken         = errors.New("admin: invalid token")
	ErrMFARequired          = errors.New("admin: MFA required")
	ErrInvalidMFA           = errors.New("admin: invalid MFA code")
	ErrAuthUnavailable      = errors.New("admin: authentication state unavailable")
	ErrInvalidAdminPassword = errors.New(AdminPasswordPolicyMessage)
)

// AdminPasswordPolicyMessage is the stable user-facing administrator password requirement.
const AdminPasswordPolicyMessage = "管理员密码至少 8 位，且必须包含英文大写字母、英文小写字母和数字"

// MFAChallengeStore atomically tracks whether a short-lived MFA login
// challenge has already been consumed. The authentication module owns this
// port; Redis is only one possible infrastructure adapter.
type MFAChallengeStore interface {
	Claim(ctx context.Context, challengeID string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, challengeID string) error
}

type AuthConfig struct {
	JWTSecret        string
	Issuer           string
	AccessTTL        time.Duration
	RefreshTTL       time.Duration
	ChallengeTTL     time.Duration
	PendingFactorTTL time.Duration
	MFAEnabled       bool
	MaxSessions      int
}

type LoginMeta struct {
	IP        string
	UserAgent string
}

type AuthResult struct {
	Stage        string `json:"stage"`
	AccessToken  string `json:"access_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	RefreshToken string `json:"-"`
	Challenge    string `json:"mfa_challenge_token,omitempty"`
	SetupToken   string `json:"mfa_setup_token,omitempty"`
}

type AccessIdentity struct {
	UserID             int64
	Email              string
	AuthRevision       int64
	Roles              []string
	MustChangePassword bool
}

type adminClaims struct {
	TokenType    string `json:"token_type"`
	AuthRevision int64  `json:"auth_revision"`
	jwt.RegisteredClaims
}

type AuthService struct {
	store          *Store
	cipher         *Cipher
	challengeStore MFAChallengeStore
	cfg            AuthConfig
	now            func() time.Time
	cfgMu          sync.RWMutex
}

func NewAuthService(store *Store, cipher *Cipher, challengeStore MFAChallengeStore, cfg AuthConfig) (*AuthService, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("admin JWT secret must be at least 32 characters")
	}
	if challengeStore == nil {
		return nil, errors.New("admin MFA challenge store is required")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "ThingConnect Admin"
	}
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 7 * 24 * time.Hour
	}
	if cfg.ChallengeTTL == 0 {
		cfg.ChallengeTTL = 5 * time.Minute
	}
	if cfg.PendingFactorTTL == 0 {
		cfg.PendingFactorTTL = 10 * time.Minute
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 10
	}
	return &AuthService{store: store, cipher: cipher, challengeStore: challengeStore, cfg: cfg, now: time.Now}, nil
}

func HashAdminPassword(password string) (string, error) {
	if err := ValidateAdminPassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// ValidateAdminPassword applies the shared policy used by every administrator entry point.
func ValidateAdminPassword(password string) error {
	if utf8.RuneCountInString(password) < 8 {
		return ErrInvalidAdminPassword
	}
	var hasUpper, hasLower, hasDigit bool
	for _, character := range password {
		switch {
		case character >= 'A' && character <= 'Z':
			hasUpper = true
		case character >= 'a' && character <= 'z':
			hasLower = true
		case character >= '0' && character <= '9':
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return ErrInvalidAdminPassword
	}
	return nil
}

func (a *AuthService) Login(ctx context.Context, email, password string, meta LoginMeta) (AuthResult, error) {
	user, err := a.store.AdminByEmail(ctx, email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(valueOrDummyHash(user)), []byte(password)) != nil {
		_ = a.store.RecordLogin(ctx, userIDOrZero(user), strings.ToLower(strings.TrimSpace(email)), meta.IP, meta.UserAgent, "invalid credentials", false)
		return AuthResult{}, ErrInvalidCredentials
	}
	if user.Status != 1 {
		_ = a.store.RecordLogin(ctx, user.ID, user.Email, meta.IP, meta.UserAgent, "account disabled", false)
		return AuthResult{}, ErrAccountDisabled
	}
	if a.MFAEnabled() {
		_, _, challengeTTL := a.TTLs()
		factor, factorErr := a.store.ActiveMFAFactor(ctx, user.ID)
		if factorErr == nil && factor != nil {
			token, err := a.issueJWT(user, "mfa_challenge", challengeTTL)
			return AuthResult{Stage: "mfa_required", Challenge: token}, err
		}
		if !errors.Is(factorErr, ErrNotFound) {
			return AuthResult{}, factorErr
		}
		a.cfgMu.RLock()
		setupTTL := a.cfg.PendingFactorTTL
		a.cfgMu.RUnlock()
		token, err := a.issueJWT(user, "mfa_setup", setupTTL)
		return AuthResult{Stage: "mfa_setup_required", SetupToken: token}, err
	}
	result, err := a.issueFullSession(ctx, user)
	if err == nil {
		_ = a.store.RecordLogin(ctx, user.ID, user.Email, meta.IP, meta.UserAgent, "success", true)
	}
	return result, err
}

func valueOrDummyHash(user *AdminUser) string {
	if user != nil {
		return user.Password
	}
	// A valid bcrypt hash keeps unknown-account and wrong-password work similar.
	return "$2a$10$7EqJtq98hPqEX7fNZaFWoO5uD9Y5YQ9b2YV7lQOjp7lT3mT7bT2dK"
}

func userIDOrZero(user *AdminUser) int64 {
	if user == nil {
		return 0
	}
	return user.ID
}

func (a *AuthService) VerifyMFA(ctx context.Context, challengeToken, code, recoveryCode string, meta LoginMeta) (AuthResult, error) {
	identity, claims, err := a.validateScopedTokenClaims(ctx, challengeToken, "mfa_challenge")
	if err != nil {
		return AuthResult{}, err
	}
	if claims.ID == "" || claims.ExpiresAt == nil {
		return AuthResult{}, ErrInvalidToken
	}
	claimTTL := claims.ExpiresAt.Sub(a.now())
	if claimTTL <= 0 {
		return AuthResult{}, ErrInvalidToken
	}
	challengeID := TokenHash(claims.ID)
	claimed, err := a.challengeStore.Claim(ctx, challengeID, claimTTL)
	if err != nil {
		return AuthResult{}, fmt.Errorf("%w: claim MFA challenge", ErrAuthUnavailable)
	}
	if !claimed {
		return AuthResult{}, ErrInvalidToken
	}
	keepClaim := false
	defer func() {
		if !keepClaim {
			_ = a.challengeStore.Release(ctx, challengeID)
		}
	}()
	factor, err := a.store.ActiveMFAFactor(ctx, identity.UserID)
	if err != nil {
		return AuthResult{}, ErrInvalidMFA
	}
	verified := false
	if strings.TrimSpace(code) != "" {
		secret, err := a.cipher.Decrypt(factor.SecretEnc, mfaAAD(identity.UserID))
		if err != nil {
			return AuthResult{}, err
		}
		step, ok := ValidateTOTP(string(secret), code, a.now(), factor.LastUsedStep)
		if ok {
			verified, err = a.store.MarkMFAStep(ctx, factor.ID, factor.LastUsedStep, step)
			if err != nil {
				return AuthResult{}, err
			}
		}
	} else if normalized := normalizeRecoveryCode(recoveryCode); normalized != "" {
		verified, err = a.store.ConsumeRecoveryCode(ctx, identity.UserID, TokenHash(normalized))
		if err != nil {
			return AuthResult{}, err
		}
	}
	if !verified {
		_ = a.store.RecordLogin(ctx, identity.UserID, identity.Email, meta.IP, meta.UserAgent, "invalid MFA", false)
		return AuthResult{}, ErrInvalidMFA
	}
	// From this point the factor or recovery code has been consumed. Retaining
	// the claim makes the challenge single-use even if session issuance fails.
	keepClaim = true
	user, err := a.store.AdminByID(ctx, identity.UserID)
	if err != nil || user.Status != 1 {
		return AuthResult{}, ErrAccountDisabled
	}
	result, err := a.issueFullSession(ctx, user)
	if err == nil {
		_ = a.store.RecordLogin(ctx, user.ID, user.Email, meta.IP, meta.UserAgent, "success", true)
	}
	return result, err
}

func (a *AuthService) EnrollTOTP(ctx context.Context, scopedToken string) (string, string, error) {
	identity, err := a.validateScopedToken(ctx, scopedToken, "mfa_setup")
	if err != nil {
		return "", "", err
	}
	user, err := a.store.AdminByID(ctx, identity.UserID)
	if err != nil {
		return "", "", err
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	encrypted, err := a.cipher.Encrypt([]byte(secret), mfaAAD(user.ID))
	if err != nil {
		return "", "", err
	}
	if err := a.store.SavePendingMFA(ctx, user.ID, encrypted); err != nil {
		return "", "", err
	}
	return secret, TOTPUri(a.cfg.Issuer, user.Email, secret), nil
}

func (a *AuthService) ConfirmTOTP(ctx context.Context, scopedToken, code string) (AuthResult, []string, error) {
	identity, err := a.validateScopedToken(ctx, scopedToken, "mfa_setup")
	if err != nil {
		return AuthResult{}, nil, err
	}
	factor, err := a.store.MFAFactor(ctx, identity.UserID)
	if err != nil || factor.Status != 0 || a.now().Sub(factor.CreatedAt) > a.cfg.PendingFactorTTL {
		return AuthResult{}, nil, ErrInvalidMFA
	}
	secret, err := a.cipher.Decrypt(factor.SecretEnc, mfaAAD(identity.UserID))
	if err != nil {
		return AuthResult{}, nil, err
	}
	step, ok := ValidateTOTP(string(secret), code, a.now(), 0)
	if !ok {
		return AuthResult{}, nil, ErrInvalidMFA
	}
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		return AuthResult{}, nil, err
	}
	hashes := make([]string, len(codes))
	for i, recovery := range codes {
		hashes[i] = TokenHash(normalizeRecoveryCode(recovery))
	}
	if err := a.store.ConfirmMFA(ctx, identity.UserID, step, hashes); err != nil {
		return AuthResult{}, nil, err
	}
	user, err := a.store.AdminByID(ctx, identity.UserID)
	if err != nil {
		return AuthResult{}, nil, err
	}
	result, err := a.issueFullSession(ctx, user)
	return result, codes, err
}

func (a *AuthService) Refresh(ctx context.Context, refreshToken string) (AuthResult, error) {
	newToken, err := RandomToken(32)
	if err != nil {
		return AuthResult{}, err
	}
	accessTTL, refreshTTL, _ := a.TTLs()
	session, err := a.store.RotateSession(ctx, TokenHash(refreshToken), TokenHash(newToken), a.now().Add(refreshTTL))
	if err != nil {
		return AuthResult{}, err
	}
	user, err := a.store.AdminByID(ctx, session.AdminUserID)
	if err != nil || user.Status != 1 {
		_ = a.store.RevokeSessions(ctx, session.AdminUserID, "account unavailable")
		return AuthResult{}, ErrAccountDisabled
	}
	access, err := a.issueJWT(user, "access", accessTTL)
	return AuthResult{Stage: "authenticated", AccessToken: access, ExpiresIn: int64(accessTTL.Seconds()), RefreshToken: newToken}, err
}

func (a *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return a.store.RevokeSessionToken(ctx, TokenHash(refreshToken), "logout")
}

// VerifyStepUp performs a fresh MFA check for high-risk administrative
// actions. TOTP steps and recovery codes are consumed exactly as at login.
func (a *AuthService) VerifyStepUp(ctx context.Context, userID int64, code, recoveryCode string) error {
	if !a.MFAEnabled() {
		return nil
	}
	factor, err := a.store.ActiveMFAFactor(ctx, userID)
	if err != nil {
		return ErrInvalidMFA
	}
	if strings.TrimSpace(code) != "" {
		secret, err := a.cipher.Decrypt(factor.SecretEnc, mfaAAD(userID))
		if err != nil {
			return err
		}
		step, ok := ValidateTOTP(string(secret), code, a.now(), factor.LastUsedStep)
		if !ok {
			return ErrInvalidMFA
		}
		used, err := a.store.MarkMFAStep(ctx, factor.ID, factor.LastUsedStep, step)
		if err != nil || !used {
			return ErrInvalidMFA
		}
		return nil
	}
	if normalized := normalizeRecoveryCode(recoveryCode); normalized != "" {
		used, err := a.store.ConsumeRecoveryCode(ctx, userID, TokenHash(normalized))
		if err != nil || !used {
			return ErrInvalidMFA
		}
		return nil
	}
	return ErrInvalidMFA
}

func (a *AuthService) MFAEnabled() bool {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.MFAEnabled
}
func (a *AuthService) SetMFAEnabled(enabled bool) {
	a.cfgMu.Lock()
	a.cfg.MFAEnabled = enabled
	a.cfgMu.Unlock()
}
func (a *AuthService) TTLs() (time.Duration, time.Duration, time.Duration) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.AccessTTL, a.cfg.RefreshTTL, a.cfg.ChallengeTTL
}
func (a *AuthService) SetSessionPolicy(accessTTL, refreshTTL time.Duration, maxSessions int) {
	a.cfgMu.Lock()
	a.cfg.AccessTTL, a.cfg.RefreshTTL = accessTTL, refreshTTL
	if maxSessions > 0 {
		a.cfg.MaxSessions = maxSessions
	}
	a.cfgMu.Unlock()
}

func (a *AuthService) VerifyPassword(ctx context.Context, userID int64, password string) error {
	user, err := a.store.AdminByID(ctx, userID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(valueOrDummyHash(user)), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}

func (a *AuthService) RegenerateRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(codes))
	for index, code := range codes {
		hashes[index] = TokenHash(normalizeRecoveryCode(code))
	}
	if err := a.store.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

func (a *AuthService) ValidateAccess(ctx context.Context, token string) (AccessIdentity, error) {
	return a.validateScopedToken(ctx, token, "access")
}

func (a *AuthService) issueFullSession(ctx context.Context, user *AdminUser) (AuthResult, error) {
	refresh, err := RandomToken(32)
	if err != nil {
		return AuthResult{}, err
	}
	accessTTL, refreshTTL, _ := a.TTLs()
	if _, err := a.store.CreateSession(ctx, user.ID, TokenHash(refresh), a.now().Add(refreshTTL)); err != nil {
		return AuthResult{}, err
	}
	a.cfgMu.RLock()
	maxSessions := a.cfg.MaxSessions
	a.cfgMu.RUnlock()
	if err := a.store.TrimSessions(ctx, user.ID, maxSessions); err != nil {
		return AuthResult{}, err
	}
	access, err := a.issueJWT(user, "access", accessTTL)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Stage: "authenticated", AccessToken: access, ExpiresIn: int64(accessTTL.Seconds()), RefreshToken: refresh}, nil
}

func (a *AuthService) issueJWT(user *AdminUser, tokenType string, ttl time.Duration) (string, error) {
	now := a.now()
	claims := adminClaims{
		TokenType: tokenType, AuthRevision: user.AuthRevision,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: a.cfg.Issuer, Subject: strconv.FormatInt(user.ID, 10),
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID: mustRandomToken(),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.cfg.JWTSecret))
}

func mustRandomToken() string {
	token, err := RandomToken(16)
	if err != nil {
		panic(err)
	}
	return token
}

func (a *AuthService) validateScopedToken(ctx context.Context, raw, expectedType string) (AccessIdentity, error) {
	identity, _, err := a.validateScopedTokenClaims(ctx, raw, expectedType)
	return identity, err
}

func (a *AuthService) validateScopedTokenClaims(ctx context.Context, raw, expectedType string) (AccessIdentity, *adminClaims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &adminClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, ErrInvalidToken
		}
		return []byte(a.cfg.JWTSecret), nil
	}, jwt.WithIssuer(a.cfg.Issuer), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !parsed.Valid {
		return AccessIdentity{}, nil, ErrInvalidToken
	}
	claims, ok := parsed.Claims.(*adminClaims)
	if !ok || claims.TokenType != expectedType {
		return AccessIdentity{}, nil, ErrInvalidToken
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return AccessIdentity{}, nil, ErrInvalidToken
	}
	user, err := a.store.AdminByID(ctx, userID)
	if err != nil || user.Status != 1 || user.AuthRevision != claims.AuthRevision {
		return AccessIdentity{}, nil, ErrInvalidToken
	}
	roles, err := a.store.AdminRoles(ctx, userID)
	if err != nil {
		return AccessIdentity{}, nil, fmt.Errorf("load admin roles: %w", err)
	}
	return AccessIdentity{UserID: userID, Email: user.Email, AuthRevision: claims.AuthRevision, Roles: roles, MustChangePassword: user.MustChangePassword == 1}, claims, nil
}

func mfaAAD(userID int64) string { return fmt.Sprintf("admin-mfa/%d/totp", userID) }

func normalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
