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
	// now es el reloj inyectable. En producción es time.Now; los tests lo
	// sustituyen para fijar la hora y verificar el cálculo de expiración.
	now func() time.Time
}

func newTokenManager(secret string, ttl time.Duration) *tokenManager {
	return &tokenManager{secret: []byte(secret), ttl: ttl, now: time.Now}
}

type jwtClaims struct {
	TenantID       string `json:"tid"`
	IsSuperAdmin   bool   `json:"sa"`
	ActiveBranchID string `json:"abid,omitempty"`
	jwt.RegisteredClaims
}

// issue emite un token con el TTL base del manager (default). Se usa en pruebas y
// flujos que no son sesión de usuario. Para una sesión de login usar issueSession.
func (tm *tokenManager) issue(c Claims) (string, time.Time, error) {
	return tm.issueUntil(c, tm.now().Add(tm.ttl))
}

// issueSession emite un token de sesión de usuario cuya expiración es la próxima
// 8:00 am hora de México (con guardia de vida mínima; ver nextSessionExpiry). La
// regla es única para todos los roles (staff y admins) para ser predecible. La
// cookie debe usar el mismo exp devuelto para que cookie y JWT exp coincidan.
func (tm *tokenManager) issueSession(c Claims) (string, time.Time, error) {
	return tm.issueUntil(c, nextSessionExpiry(tm.now()))
}

// issueUntil emite un token firmado que expira exactamente en exp. iat = tm.now().
// Solo cambia el cálculo del exp respecto al flujo anterior: claims y firma son
// idénticos.
func (tm *tokenManager) issueUntil(c Claims, exp time.Time) (string, time.Time, error) {
	now := tm.now()

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
