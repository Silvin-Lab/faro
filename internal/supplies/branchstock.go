package supplies

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UpsertBranchStock aplica delta (FIRMADO) al cache de existencias de una sucursal
// (supply_branch_stock) DENTRO de la transacción dada, con el mismo mecanismo que
// insertMovement (ON CONFLICT (supply_id, branch_id) DO UPDATE ... += delta).
// Devuelve el nuevo stock_base.
//
// Se exporta para que el módulo de almacén (salida/dispatch y merma de sucursal)
// reutilice EXACTAMENTE el mismo upsert en su propia transacción, en vez de
// duplicarlo — manteniendo una sola definición del cruce cache↔ledger de sucursal.
// No valida existencias: el stock puede quedar negativo (patrón no bloqueante, R4).
func UpsertBranchStock(ctx context.Context, tx pgx.Tx, tenantID, supplyID, branchID string, delta int) (int, error) {
	var stockBase int
	err := tx.QueryRow(ctx,
		`INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (supply_id, branch_id)
		 DO UPDATE SET stock_base = supply_branch_stock.stock_base + EXCLUDED.stock_base,
		               updated_at = now()
		 RETURNING stock_base`,
		tenantID, supplyID, branchID, delta).Scan(&stockBase)
	return stockBase, err
}
