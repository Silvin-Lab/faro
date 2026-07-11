package auth

import (
	"time"

	// tzdata embebe la base de datos de zonas horarias de la IANA en el binario.
	// La imagen de producción es distroless y NO trae /usr/share/zoneinfo, por lo
	// que sin este import time.LoadLocation("America/Mexico_City") fallaría en
	// runtime. Al importarlo la tzdata queda dentro del binario y LoadLocation
	// funciona sin depender del sistema operativo.
	_ "time/tzdata"
)

// mexicoTZ es la zona horaria de referencia para la expiración de sesión. México
// no observa horario de verano desde 2022 (offset estable UTC-6), pero usamos la
// zona nombrada por corrección en lugar de un offset hardcodeado.
const mexicoTZ = "America/Mexico_City"

// sessionExpiryHour es la hora local (America/Mexico_City) a la que expiran TODAS
// las sesiones, sin importar el rol. Un login por la mañana cubre el turno; a las
// 8:00 am del día siguiente el usuario debe volver a iniciar sesión.
const sessionExpiryHour = 8

// minSessionLife es la guardia de vida mínima. Si la próxima 8:00 am está a menos
// de este margen respecto a "now" (p. ej. alguien entra de madrugada o justo antes
// de las 8), la expiración salta a la 8:00 am del día siguiente para evitar
// sesiones absurdamente cortas.
const minSessionLife = 6 * time.Hour

// mexicoLocation carga la zona horaria de México. Con time/tzdata embebido esto no
// depende del SO; el fallback a un offset fijo UTC-6 es defensivo y no debería
// ejecutarse jamás en la práctica.
func mexicoLocation() *time.Location {
	loc, err := time.LoadLocation(mexicoTZ)
	if err != nil {
		return time.FixedZone("CST", -6*60*60)
	}
	return loc
}

// nextSessionExpiry devuelve el instante de expiración de una sesión emitida en
// "now": la próxima 8:00:00 am hora de México estrictamente posterior a "now",
// respetando la guardia de vida mínima (si la 8am más cercana está a <6h, usa la
// del día siguiente). Es una función pura (recibe el reloj) para poder testearla
// de forma determinista.
func nextSessionExpiry(now time.Time) time.Time {
	loc := mexicoLocation()
	local := now.In(loc)

	// Candidato: hoy a las 8:00:00 am hora local.
	exp := time.Date(local.Year(), local.Month(), local.Day(), sessionExpiryHour, 0, 0, 0, loc)

	// Avanza día a día hasta que la expiración sea estrictamente posterior a "now"
	// Y esté al menos minSessionLife en el futuro. Como la 8am ocurre una vez al
	// día y minSessionLife < 24h, el bucle itera a lo sumo dos veces.
	for !exp.After(now) || exp.Sub(now) < minSessionLife {
		exp = exp.AddDate(0, 0, 1)
	}
	return exp
}
