package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims son los datos de sesión que viajan en el JWT.
type Claims struct {
	UserID         string
	TenantID       *string
	IsSuperAdmin   bool
	ActiveBranchID *string // sucursal activa de la sesión (ADR-007 §D4); nil si no hay
}

type tokenManager struct {
	secret []byte
	ttl    time.Duration
}

func newTokenManager(secret string, ttl time.Duration) *tokenManager {
	return &tokenManager{secret: []byte(secret), ttl: ttl}
}

type jwtClaims struct {
	TenantID       string `json:"tid"`
	IsSuperAdmin   bool   `json:"sa"`
	ActiveBranchID string `json:"abid,omitempty"`
	jwt.RegisteredClaims
}

func (tm *tokenManager) issue(c Claims) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(tm.ttl)

	tid := ""
	if c.TenantID != nil {
		tid = *c.TenantID
	}
	abid := ""
	if c.ActiveBranchID != nil {
		abid = *c.ActiveBranchID
	}

	claims := jwtClaims{
		TenantID:       tid,
		IsSuperAdmin:   c.IsSuperAdmin,
		ActiveBranchID: abid,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   c.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(tm.secret)
	return signed, exp, err
}

func (tm *tokenManager) parse(tokenStr string) (Claims, error) {
	var jc jwtClaims
	_, err := jwt.ParseWithClaims(tokenStr, &jc, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return tm.secret, nil
	})
	if err != nil {
		return Claims{}, err
	}

	var tid *string
	if jc.TenantID != "" {
		v := jc.TenantID
		tid = &v
	}
	var abid *string
	if jc.ActiveBranchID != "" {
		v := jc.ActiveBranchID
		abid = &v
	}
	return Claims{UserID: jc.Subject, TenantID: tid, IsSuperAdmin: jc.IsSuperAdmin, ActiveBranchID: abid}, nil
}
