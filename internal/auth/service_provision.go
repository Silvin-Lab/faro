package auth

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrEmailTaken     = errors.New("email taken")
	ErrValidation     = errors.New("validation")
	ErrTenantRequired = errors.New("tenant required")
	ErrTenantNotFound = errors.New("tenant not found")
	ErrBranchNotFound = errors.New("branch not found")
)

// UserPatch son los cambios opcionales de un usuario (PATCH /users/{id}). Name nil
// = no cambia. BranchIDs nil = no cambia; si se indica, reemplaza el set completo de
// membresías (M:N) y debe traer ≥1 sucursal del negocio.
type UserPatch struct {
	Name      *string
	Role      *string
	BranchIDs *[]string
}

// CreateTenantInput son los datos para dar de alta un negocio con su dueño.
type CreateTenantInput struct {
	Name          string
	OwnerEmail    string
	OwnerPassword string
	OwnerName     string
}

// CreateTenantWithOwner valida y crea el negocio + el usuario dueño (transaccional).
// Se conserva para el seed/bootstrap del negocio único (POST /tenants se retiró).
func (svc *Service) CreateTenantWithOwner(ctx context.Context, in CreateTenantInput) (Tenant, User, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.OwnerEmail = strings.TrimSpace(in.OwnerEmail)
	in.OwnerName = strings.TrimSpace(in.OwnerName)
	if in.Name == "" || in.OwnerName == "" || !validEmail(in.OwnerEmail) || len(in.OwnerPassword) < minPasswordLen {
		return Tenant{}, User{}, ErrValidation
	}
	hash, err := hashPassword(in.OwnerPassword)
	if err != nil {
		return Tenant{}, User{}, err
	}
	return svc.store.createTenantWithOwner(ctx, in.Name, in.OwnerEmail, in.OwnerName, hash)
}

// CreateUser valida y crea un usuario según su rol (M8). Un role='super_admin'
// crea un admin global (tenant NULL, is_super_admin, sin sucursales) y NO acepta
// branchIds. Los roles de sucursal exigen ≥1 sucursal del tenant del negocio.
func (svc *Service) CreateUser(ctx context.Context, tenantID, email, password, name, role string, branchIDs []string) (User, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	role = strings.TrimSpace(role)
	branchIDs = dedupeNonEmpty(branchIDs)
	if name == "" || !validEmail(email) || len(password) < minPasswordLen || !validRole(role) {
		return User{}, ErrValidation
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	if role == RoleSuperAdmin {
		// Identidad global: no admite membresías de sucursal.
		if len(branchIDs) != 0 {
			return User{}, ErrValidation
		}
		return svc.store.createSuperAdminUser(ctx, email, name, hash)
	}
	if role == RoleRepostero {
		// Repostero (M10, F21): tenant-scoped SIN sucursal. NO admite membresías (si
		// trae alguna => ErrValidation) y se crea con el tenant del negocio.
		if len(branchIDs) != 0 {
			return User{}, ErrValidation
		}
		return svc.store.createReposteroUser(ctx, tenantID, email, name, hash)
	}
	// Roles de sucursal: exigen ≥1 sucursal del negocio.
	if len(branchIDs) == 0 {
		return User{}, ErrValidation
	}
	if err := svc.assertBranchesOwned(ctx, tenantID, branchIDs); err != nil {
		return User{}, err
	}
	return svc.store.createUser(ctx, tenantID, email, name, hash, role, branchIDs)
}

// UpdateUser aplica cambios (nombre y/o membresías) a un usuario del negocio. Si se
// indica branchIds, reemplaza el set completo (debe traer ≥1 sucursal del negocio).
func (svc *Service) UpdateUser(ctx context.Context, tenantID, id string, patch UserPatch) (User, error) {
	if patch.Name != nil {
		n := strings.TrimSpace(*patch.Name)
		if n == "" {
			return User{}, ErrValidation
		}
		patch.Name = &n
	}
	// Blindaje del invariante "repostero sin sucursal" (R4, F21): si el usuario objetivo
	// es repostero, NO se le puede asignar sucursal ni cambiarle el rol por esta vía. Se
	// carga el rol actual solo cuando el patch podría violarlo (role o branchIDs). El guard
	// de patch.Role de abajo ya impide PROMOVER a repostero (solo isBranchRole pasa); esto
	// cubre el lado que faltaba: no colgarle una sucursal ni degradarlo a rol de sucursal.
	if patch.Role != nil || patch.BranchIDs != nil {
		current, err := svc.store.roleOfUser(ctx, tenantID, id)
		if err != nil {
			return User{}, err
		}
		if current == RoleRepostero {
			return User{}, ErrValidation
		}
	}
	if patch.Role != nil {
		r := strings.TrimSpace(*patch.Role)
		// Solo roles de sucursal por esta vía: no se puede promover a super_admin
		// (cambio de identidad). El super admin, además, no vive bajo un tenant, así
		// que jamás cae en esta ruta (tenant-scoped => ErrNotFound).
		if !isBranchRole(r) {
			return User{}, ErrValidation
		}
		patch.Role = &r
	}
	if patch.BranchIDs != nil {
		ids := dedupeNonEmpty(*patch.BranchIDs)
		if len(ids) == 0 {
			return User{}, ErrValidation
		}
		if err := svc.assertBranchesOwned(ctx, tenantID, ids); err != nil {
			return User{}, err
		}
		patch.BranchIDs = &ids
	}
	return svc.store.updateUser(ctx, tenantID, id, patch)
}

// ListUsers devuelve los usuarios del negocio con sus membresías (branches[]).
func (svc *Service) ListUsers(ctx context.Context, tenantID string) ([]User, error) {
	return svc.store.listUsersByTenant(ctx, tenantID)
}

// assertBranchesOwned falla con ErrBranchNotFound si alguna branch no es del tenant.
func (svc *Service) assertBranchesOwned(ctx context.Context, tenantID string, ids []string) error {
	owned, err := svc.store.countOwnedBranches(ctx, tenantID, ids)
	if err != nil {
		return err
	}
	if owned != len(ids) {
		return ErrBranchNotFound
	}
	return nil
}

// dedupeNonEmpty limpia, descarta vacíos y deduplica una lista de ids.
func dedupeNonEmpty(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		v := strings.TrimSpace(id)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
