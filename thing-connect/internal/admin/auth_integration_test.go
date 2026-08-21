package admin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestMFAChallengeSingleUseAndPendingFactorReenrollment(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := mysqlmigrate.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}

	ctx := context.Background()
	email := fmt.Sprintf("mfa-replay-%d@example.com", time.Now().UnixNano())
	password := "AdminChallenge123!"
	passwordHash, err := HashAdminPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sqlDB.ExecContext(ctx, `INSERT INTO admin_users (email,password,nick_name) VALUES (?,?,?)`, email, passwordHash, "MFA replay test")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_sessions WHERE admin_user_id=?`, userID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_login_log WHERE admin_user_id=?`, userID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id=?`, userID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_mfa_factors WHERE admin_user_id=?`, userID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_users WHERE id=?`, userID)
	})

	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO admin_mfa_factors (admin_user_id,factor_type,secret_enc,status) VALUES (?,'totp','unused-for-recovery-code',1)`, userID); err != nil {
		t.Fatal(err)
	}
	recoverySuffix, err := RandomToken(16)
	if err != nil {
		t.Fatal(err)
	}
	firstRecovery, secondRecovery := "recover-one-"+recoverySuffix, "recover-two-"+recoverySuffix
	for _, code := range []string{firstRecovery, secondRecovery} {
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO admin_mfa_recovery_codes (admin_user_id,code_hash) VALUES (?,?)`, userID, TokenHash(normalizeRecoveryCode(code))); err != nil {
			t.Fatal(err)
		}
	}

	cipher, err := NewCipher("test-v1", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	challengeStore := newMemoryMFAChallengeStore()
	auth, err := NewAuthService(NewStore(sqlDB), cipher, challengeStore, AuthConfig{
		JWTSecret:  "01234567890123456789012345678901",
		MFAEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := auth.Login(ctx, email, password, LoginMeta{IP: "127.0.0.1"})
	if err != nil || login.Stage != "mfa_required" || login.Challenge == "" {
		t.Fatalf("Login = %+v, %v", login, err)
	}

	// A failed factor check releases the claim so the administrator can retry
	// the same still-valid challenge.
	if _, err := auth.VerifyMFA(ctx, login.Challenge, "", "wrong-code", LoginMeta{}); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("wrong recovery code error = %v, want ErrInvalidMFA", err)
	}
	verified, err := auth.VerifyMFA(ctx, login.Challenge, "", firstRecovery, LoginMeta{})
	if err != nil || verified.Stage != "authenticated" {
		t.Fatalf("VerifyMFA = %+v, %v", verified, err)
	}

	// Even another unused recovery code cannot replay a successfully consumed
	// challenge token.
	if _, err := auth.VerifyMFA(ctx, login.Challenge, "", secondRecovery, LoginMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replayed challenge error = %v, want ErrInvalidToken", err)
	}
	var secondUsed int
	if err := sqlDB.GetContext(ctx, &secondUsed, `SELECT COUNT(*) FROM admin_mfa_recovery_codes WHERE admin_user_id=? AND code_hash=? AND used_at IS NOT NULL`, userID, TokenHash(normalizeRecoveryCode(secondRecovery))); err != nil {
		t.Fatal(err)
	}
	if secondUsed != 0 {
		t.Fatal("replayed challenge consumed a second recovery code")
	}

	// Re-enrolling replaces an expired pending factor and restarts its ten-minute
	// confirmation window by refreshing created_at.
	if _, err := sqlDB.ExecContext(ctx, `UPDATE admin_mfa_factors SET status=0,created_at=NOW()-INTERVAL 1 HOUR WHERE admin_user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(sqlDB).SavePendingMFA(ctx, userID, "replacement-secret"); err != nil {
		t.Fatal(err)
	}
	var ageSeconds int
	if err := sqlDB.GetContext(ctx, &ageSeconds, `SELECT TIMESTAMPDIFF(SECOND,created_at,NOW()) FROM admin_mfa_factors WHERE admin_user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if ageSeconds > 5 {
		t.Fatalf("pending MFA created_at was not refreshed, age=%ds", ageSeconds)
	}
}
