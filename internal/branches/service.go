package branches

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrValidation indica entrada inválida (nombre vacío/largo, status inválido).
var ErrValidation = errors.New("validation")

const maxNameLen = 60

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// List devuelve las sucursales del negocio. status: "active" | "inactive" | "" (todas).
func (svc *Service) List(ctx context.Context, tenantID, status string) ([]Branch, error) {
	switch status {
	case "active", "inactive":
	default:
		status = ""
	}
	return svc.store.list(ctx, tenantID, status)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Branch, error) {
	return svc.store.get(ctx, tenantID, id)
}

// Create valida el nombre y crea la sucursal. ErrValidation si el nombre es
// vacío o excede el límite; ErrNameTaken si ya existe en el negocio.
func (svc *Service) Create(ctx context.Context, tenantID, name string) (Branch, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Branch{}, ErrValidation
	}
	return svc.store.create(ctx, tenantID, name)
}

// Update renombra y/o cambia el estado. Ambos campos son opcionales.
func (svc *Service) Update(ctx context.Context, tenantID, id string, patch BranchPatch) (Branch, error) {
	if patch.Name != nil {
		n := strings.TrimSpace(*patch.Name)
		if !validName(n) {
			return Branch{}, ErrValidation
		}
		patch.Name = &n
	}
	if patch.Status != nil {
		if *patch.Status != "active" && *patch.Status != "inactive" {
			return Branch{}, ErrValidation
		}
	}
	if patch.Name == nil && patch.Status == nil {
		// Nada que actualizar: devolver el recurso actual (tenant-scoped).
		return svc.store.get(ctx, tenantID, id)
	}
	return svc.store.update(ctx, tenantID, id, patch)
}

// Delete borra físicamente la sucursal. ErrInUse si hay usuarios o ventas que la
// referencian (la UI debe preferir desactivar).
func (svc *Service) Delete(ctx context.Context, tenantID, id string) error {
	return svc.store.delete(ctx, tenantID, id)
}

func validName(name string) bool {
	return name != "" && len(name) <= maxNameLen
}
