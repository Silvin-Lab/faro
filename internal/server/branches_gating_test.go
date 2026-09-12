package server_test

import (
	"net/http"
	"testing"
)

// TestBranchesRepostero verifica que el split del router abre GET /branches al repostero
// (200) manteniendo POST/PATCH/DELETE en 403 para él; y que los 3 roles de sucursal siguen
// en 403 en TODO /branches (incl. GET /) mientras super_admin conserva acceso total.
func TestBranchesRepostero(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")

	// Repostero: GET /branches => 200 (necesita el filtro por sucursal).
	if resp := env.do(t, repo, http.MethodGet, "/branches", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("repostero GET /branches: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Repostero: escritura de /branches => 403.
	repoForbidden := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/branches", map[string]any{"name": "Nueva"}},
		{http.MethodPatch, "/branches/" + b1, map[string]any{"name": "Renombrada"}},
		{http.MethodDelete, "/branches/" + b1, nil},
	}
	for _, rt := range repoForbidden {
		if resp := env.do(t, repo, rt.method, rt.path, rt.body); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("repostero %s %s: esperaba 403, obtuvo %d", rt.method, rt.path, resp.StatusCode)
		}
	}

	// Los 3 roles de sucursal: 403 en TODO /branches, incl. GET /.
	for _, role := range []string{"branch_admin", "cashier", "barista"} {
		email := role + "@vanta.test"
		env.newBranchUser(t, root, email, role, []string{b1})
		c := newClient(t)
		env.login(t, c, email, "secret123")
		if resp := env.do(t, c, http.MethodGet, "/branches", nil); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s GET /branches: esperaba 403, obtuvo %d", role, resp.StatusCode)
		}
		if resp := env.do(t, c, http.MethodPost, "/branches", map[string]any{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s POST /branches: esperaba 403, obtuvo %d", role, resp.StatusCode)
		}
	}

	// super_admin conserva acceso total: GET /branches => 200.
	if resp := env.do(t, root, http.MethodGet, "/branches", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("super_admin GET /branches: esperaba 200, obtuvo %d", resp.StatusCode)
	}
}
