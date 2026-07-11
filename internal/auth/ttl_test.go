package auth

import (
	"testing"
	"time"
)

// TestMexicoTZDataEmbedded verifica que la tzdata quedó embebida en el binario
// (import _ "time/tzdata"): LoadLocation de la zona de México no debe fallar aun
// sin la base de zonas del SO (caso imagen distroless).
func TestMexicoTZDataEmbedded(t *testing.T) {
	loc, err := time.LoadLocation(mexicoTZ)
	if err != nil {
		t.Fatalf("LoadLocation(%q) falló: %v (¿falta import _ \"time/tzdata\"?)", mexicoTZ, err)
	}
	if loc.String() != mexicoTZ {
		t.Fatalf("zona cargada = %q, esperaba %q", loc.String(), mexicoTZ)
	}
	// México sin horario de verano desde 2022: offset estable UTC-6.
	ref := time.Date(2026, 7, 6, 12, 0, 0, 0, loc)
	if _, off := ref.Zone(); off != -6*60*60 {
		t.Fatalf("offset = %ds, esperaba -21600 (UTC-6)", off)
	}
}

// TestNextSessionExpiry cubre el cálculo de expiración = próxima 8:00 am local con
// la guardia de vida mínima de 6h. El reloj se inyecta como argumento.
func TestNextSessionExpiry(t *testing.T) {
	loc, err := time.LoadLocation(mexicoTZ)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, loc)
	}

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			// (a) Login a las 10am → 8am del día siguiente (22h de vida).
			name: "login 10am -> 8am mañana",
			now:  at(2026, 7, 6, 10, 0),
			want: at(2026, 7, 7, 8, 0),
		},
		{
			// (b) Login a las 6am → la 8am de hoy está a solo 2h (<6h), así que la
			// guardia empuja a la 8am del día siguiente.
			name: "login 6am -> 8am mañana (guardia 6h)",
			now:  at(2026, 7, 6, 6, 0),
			want: at(2026, 7, 7, 8, 0),
		},
		{
			// Justo antes de las 8am: 7:59 → 8am de hoy está a 1min (<6h) → mañana.
			name: "login 7:59am -> 8am mañana (guardia 6h)",
			now:  at(2026, 7, 6, 7, 59),
			want: at(2026, 7, 7, 8, 0),
		},
		{
			// Madrugada temprana: 1am → 8am de hoy está a 7h (>=6h) → 8am de hoy.
			name: "login 1am -> 8am hoy",
			now:  at(2026, 7, 6, 1, 0),
			want: at(2026, 7, 6, 8, 0),
		},
		{
			// Exactamente a las 8am: no es estrictamente posterior → 8am mañana.
			name: "login 8am exacto -> 8am mañana",
			now:  at(2026, 7, 6, 8, 0),
			want: at(2026, 7, 7, 8, 0),
		},
		{
			// Cruce de fin de mes: 30 jun 10pm → 1 jul 8am.
			name: "login fin de mes",
			now:  at(2026, 6, 30, 22, 0),
			want: at(2026, 7, 1, 8, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextSessionExpiry(tc.now)
			if !got.Equal(tc.want) {
				t.Fatalf("nextSessionExpiry(%v) = %v, esperaba %v", tc.now, got, tc.want)
			}
			// Invariantes: estrictamente futuro y al menos la guardia de vida.
			if !got.After(tc.now) {
				t.Fatalf("exp %v no es posterior a now %v", got, tc.now)
			}
			if got.Sub(tc.now) < minSessionLife {
				t.Fatalf("vida de sesión %v < mínimo %v", got.Sub(tc.now), minSessionLife)
			}
			// La expiración siempre debe caer a las 8:00:00 hora local.
			l := got.In(loc)
			if l.Hour() != sessionExpiryHour || l.Minute() != 0 || l.Second() != 0 {
				t.Fatalf("exp local = %v, esperaba %02d:00:00", l, sessionExpiryHour)
			}
		})
	}
}

// TestIssueSessionUsesExpiry verifica que el token de sesión usa nextSessionExpiry
// y que el exp devuelto (para la cookie) coincide con el JWT.
func TestIssueSessionUsesExpiry(t *testing.T) {
	loc, _ := time.LoadLocation(mexicoTZ)
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, loc)

	tm := newTokenManager("secret", time.Hour)
	tm.now = func() time.Time { return now } // reloj inyectado

	_, exp, err := tm.issueSession(Claims{UserID: "u1"})
	if err != nil {
		t.Fatalf("issueSession: %v", err)
	}
	want := time.Date(2026, 7, 7, 8, 0, 0, 0, loc)
	if !exp.Equal(want) {
		t.Fatalf("exp = %v, esperaba %v", exp, want)
	}
}
