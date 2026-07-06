package auth

import "net/mail"

const minPasswordLen = 8

// Roles del sistema (M8). super_admin es identidad global; branch_admin/cashier/
// barista son usuarios de sucursal (branch_admin además ve reportes de su sucursal).
const (
	RoleSuperAdmin  = "super_admin"
	RoleBranchAdmin = "branch_admin"
	RoleCashier     = "cashier"
	RoleBarista     = "barista"
)

func validEmail(s string) bool {
	_, err := mail.ParseAddress(s)
	return err == nil
}

// validRole indica si el rol es uno de los soportados.
func validRole(r string) bool {
	switch r {
	case RoleSuperAdmin, RoleBranchAdmin, RoleCashier, RoleBarista:
		return true
	}
	return false
}

// isBranchRole indica si el rol es de sucursal (no super admin).
func isBranchRole(r string) bool {
	return r == RoleBranchAdmin || r == RoleCashier || r == RoleBarista
}
