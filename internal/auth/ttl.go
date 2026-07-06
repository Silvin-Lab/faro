package auth

import "time"

// Duración de sesión por rol. El personal de mostrador (cashier/barista) opera un
// turno largo pero acotado; los administradores (branch_admin/super_admin) tienen
// sesiones más duraderas para tareas de gestión sin re-login frecuente.
const (
	sessionTTLBranchStaff = 20 * time.Hour     // cashier, barista
	sessionTTLAdmin       = 7 * 24 * time.Hour // branch_admin, super_admin (168h)
)

// sessionTTLFor devuelve la duración de sesión que corresponde a un rol. Para un
// rol desconocido usa el TTL más corto (default seguro).
func sessionTTLFor(role string) time.Duration {
	switch role {
	case RoleBranchAdmin, RoleSuperAdmin:
		return sessionTTLAdmin
	case RoleCashier, RoleBarista:
		return sessionTTLBranchStaff
	default:
		return sessionTTLBranchStaff
	}
}
