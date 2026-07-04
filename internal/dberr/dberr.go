// Package dberr clasifica errores de PostgreSQL (pgx) de forma reutilizable.
package dberr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

func code(err error, c string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == c
}

// IsUniqueViolation indica una violación de restricción única (23505).
func IsUniqueViolation(err error) bool { return code(err, "23505") }

// IsInvalidText indica un valor con formato inválido, p. ej. uuid mal formado (22P02).
func IsInvalidText(err error) bool { return code(err, "22P02") }

// IsForeignKeyViolation indica una violación de clave foránea (23503), p. ej.
// intentar borrar una fila referenciada por otra tabla.
func IsForeignKeyViolation(err error) bool { return code(err, "23503") }
