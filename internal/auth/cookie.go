package auth

import (
	"net/http"
	"time"
)

const sessionCookieName = "faro_session"

// sameSite decide el modo de la cookie de sesión. En producción el frontend y la
// API viven en dominios distintos (cross-site) → se requiere None+Secure para que
// la cookie viaje en las peticiones con credenciales. En dev local (mismo host,
// sin HTTPS) se usa Lax, ya que None exige Secure.
func (svc *Service) sameSite() http.SameSite {
	if svc.cookieSecure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func (svc *Service) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   svc.cookieSecure,
		SameSite: svc.sameSite(),
	})
}

func (svc *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   svc.cookieSecure,
		SameSite: svc.sameSite(),
	})
}
