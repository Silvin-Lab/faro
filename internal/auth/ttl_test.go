package auth

import (
	"testing"
	"time"
)

func TestSessionTTLFor(t *testing.T) {
	cases := map[string]time.Duration{
		RoleCashier:     20 * time.Hour,
		RoleBarista:     20 * time.Hour,
		RoleBranchAdmin: 7 * 24 * time.Hour,
		RoleSuperAdmin:  7 * 24 * time.Hour,
		"desconocido":   20 * time.Hour, // default seguro
	}
	for role, want := range cases {
		if got := sessionTTLFor(role); got != want {
			t.Errorf("sessionTTLFor(%q) = %v, esperaba %v", role, got, want)
		}
	}
}

func TestTokenIssueForUsesGivenTTL(t *testing.T) {
	tm := newTokenManager("secret", time.Hour) // TTL base distinto del pedido
	_, exp, err := tm.issueFor(Claims{UserID: "u1"}, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("issueFor: %v", err)
	}
	want := time.Now().Add(7 * 24 * time.Hour)
	if d := exp.Sub(want); d < -time.Minute || d > time.Minute {
		t.Fatalf("exp = %v, esperaba ≈ %v (delta %v)", exp, want, d)
	}
}
