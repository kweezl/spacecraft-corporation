package bases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

type pgRepository struct {
	pool *pgxpool.Pool
}

func newRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// liveScopeFmt restricts (alias) to live bases in an Ownership. It always binds
// three positional args — server id ($1), kind ($2), owner ($3) — so callers can
// append further args from $4 without renumbering. The "$2 = 'corp'" arm lets
// the same predicate serve both tiers: corp rows ignore the (NULL) owner, member
// rows require it. Use scopeArgs to supply the three args.
const liveScopeFmt = `%[1]s.servers_id = $1 AND %[1]s.deleted_at IS NULL ` +
	`AND %[1]s.kind = $2 AND (%[1]s.owner_user_id = $3 OR $2 = 'corp')`

func liveScope(alias string) string { return fmt.Sprintf(liveScopeFmt, alias) }

// scopeArgs returns the three positional args liveScope binds, in order.
func scopeArgs(o Ownership) []any { return []any{o.ServerID, string(o.Kind), o.OwnerUserID} }

// ownerArg renders the owner_user_id column value for an insert: NULL for a corp
// base (the DB check constraint requires it), the member id otherwise.
func ownerArg(kind Kind, ownerUserID string) any {
	if kind == KindCorp {
		return nil
	}
	return ownerUserID
}

func (r *pgRepository) Register(ctx context.Context, in RegisterInput, limit int) (uuid.UUID, error) {
	id, err := uuidv7.NewUUID()
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now()

	// Count + insert in one transaction so the limit can't be raced past.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	o := Ownership{ServerID: in.ServerID, Kind: in.Kind, OwnerUserID: in.OwnerUserID}
	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM bases b WHERE `+liveScope("b"), scopeArgs(o)...,
	).Scan(&count); err != nil {
		return uuid.Nil, err
	}
	if count >= limit {
		return uuid.Nil, ErrLimitReached
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO bases
			(id, servers_id, kind, owner_user_id, name, sector_name, system_code,
			 planet_number, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id, in.ServerID, string(in.Kind), ownerArg(in.Kind, in.OwnerUserID),
		in.Name, in.SectorName, in.SystemCode, in.PlanetNumber, in.CreatedByUserID, now,
	); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (r *pgRepository) DeleteOne(ctx context.Context, o Ownership, baseID uuid.UUID) (int, error) {
	now := time.Now()
	args := append(scopeArgs(o), now, baseID) // $4 = now, $5 = baseID
	tag, err := r.pool.Exec(ctx,
		`UPDATE bases AS b SET deleted_at = $4, updated_at = $4 WHERE `+liveScope("b")+` AND b.id = $5`,
		args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *pgRepository) DeleteAll(ctx context.Context, o Ownership) (int, error) {
	now := time.Now()
	args := append(scopeArgs(o), now) // $4 = now
	tag, err := r.pool.Exec(ctx,
		`UPDATE bases AS b SET deleted_at = $4, updated_at = $4 WHERE `+liveScope("b"),
		args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *pgRepository) ListOwned(ctx context.Context, o Ownership, limit int) ([]Base, error) {
	args := append(scopeArgs(o), limit) // $4 = limit
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.name, b.sector_name, b.system_code, b.planet_number
		FROM bases b WHERE `+liveScope("b")+`
		ORDER BY b.name, b.id LIMIT $4`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Base
	for rows.Next() {
		var b Base
		if err := rows.Scan(&b.ID, &b.Name, &b.SectorName, &b.SystemCode, &b.PlanetNumber); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ownsBaseTx reports whether the ownership scope contains the given live base,
// taking a row lock so a concurrent equipment insert sees a consistent count.
func ownsBaseTx(ctx context.Context, tx pgx.Tx, o Ownership, baseID uuid.UUID) (bool, error) {
	args := append(scopeArgs(o), baseID) // $4 = baseID
	var found uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT b.id FROM bases b WHERE `+liveScope("b")+` AND b.id = $4 FOR UPDATE`, args...,
	).Scan(&found)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// addEquipment is the shared add path for extractors and productions: verify
// ownership, enforce the per-base limit, and insert — atomically.
func (r *pgRepository) addEquipment(ctx context.Context, o Ownership, baseID uuid.UUID, table, valueCol, value string, limit int) error {
	id, err := uuidv7.NewUUID()
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	owns, err := ownsBaseTx(ctx, tx, o, baseID)
	if err != nil {
		return err
	}
	if !owns {
		return ErrBaseNotFound
	}

	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM `+table+` WHERE bases_id = $1`, baseID,
	).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrLimitReached
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO `+table+` (id, bases_id, `+valueCol+`, created_at) VALUES ($1, $2, $3, $4)`,
		id, baseID, value, time.Now(),
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgRepository) AddExtractor(ctx context.Context, o Ownership, baseID uuid.UUID, resourceName string, limit int) error {
	return r.addEquipment(ctx, o, baseID, "base_extractors", "resource_name", resourceName, limit)
}

func (r *pgRepository) AddProduction(ctx context.Context, o Ownership, baseID uuid.UUID, itemName string, limit int) error {
	return r.addEquipment(ctx, o, baseID, "base_productions", "item_name", itemName, limit)
}

// removeEquipment deletes one equipment row by id, but only when it belongs to a
// base in the ownership scope (the join enforces it). Returns rows affected.
func (r *pgRepository) removeEquipment(ctx context.Context, o Ownership, table string, equipID uuid.UUID) (int, error) {
	args := append(scopeArgs(o), equipID) // $4 = equipID
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM `+table+` AS e USING bases b
		 WHERE e.id = $4 AND e.bases_id = b.id AND `+liveScope("b"), args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *pgRepository) RemoveExtractor(ctx context.Context, o Ownership, extractorID uuid.UUID) (int, error) {
	return r.removeEquipment(ctx, o, "base_extractors", extractorID)
}

func (r *pgRepository) RemoveProduction(ctx context.Context, o Ownership, productionID uuid.UUID) (int, error) {
	return r.removeEquipment(ctx, o, "base_productions", productionID)
}

func (r *pgRepository) ListExtractors(ctx context.Context, o Ownership, baseID uuid.UUID) ([]Extractor, error) {
	args := append(scopeArgs(o), baseID) // $4 = baseID
	rows, err := r.pool.Query(ctx,
		`SELECT e.id, e.resource_name FROM base_extractors e JOIN bases b ON e.bases_id = b.id
		 WHERE `+liveScope("b")+` AND b.id = $4 ORDER BY e.created_at, e.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Extractor
	for rows.Next() {
		var e Extractor
		if err := rows.Scan(&e.ID, &e.ResourceName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *pgRepository) ListProductions(ctx context.Context, o Ownership, baseID uuid.UUID) ([]Production, error) {
	args := append(scopeArgs(o), baseID) // $4 = baseID
	rows, err := r.pool.Query(ctx,
		`SELECT p.id, p.item_name FROM base_productions p JOIN bases b ON p.bases_id = b.id
		 WHERE `+liveScope("b")+` AND b.id = $4 ORDER BY p.created_at, p.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Production
	for rows.Next() {
		var p Production
		if err := rows.Scan(&p.ID, &p.ItemName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *pgRepository) List(ctx context.Context, serverID uuid.UUID, f Filter, limit, offset int) ([]Base, int, error) {
	// Build the filter predicate incrementally so each clause binds the next
	// positional arg. The base scope (server + live) is always present.
	conds := []string{"b.servers_id = $1", "b.deleted_at IS NULL"}
	args := []any{serverID}
	like := func(col, val string) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("%s ILIKE '%%' || $%d || '%%'", col, len(args)))
	}
	if f.SectorName != "" {
		like("b.sector_name", f.SectorName)
	}
	if f.SystemCode != "" {
		like("b.system_code", f.SystemCode)
	}
	if f.BaseName != "" {
		like("b.name", f.BaseName)
	}
	if f.OwnerUserID != "" {
		args = append(args, f.OwnerUserID)
		conds = append(conds, fmt.Sprintf("b.owner_user_id = $%d", len(args)))
	}
	if f.Resource != "" {
		args = append(args, f.Resource)
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM base_extractors e WHERE e.bases_id = b.id AND e.resource_name ILIKE '%%' || $%d || '%%')",
			len(args)))
	}
	if f.Item != "" {
		args = append(args, f.Item)
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM base_productions p WHERE p.bases_id = b.id AND p.item_name ILIKE '%%' || $%d || '%%')",
			len(args)))
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM bases b WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT b.id, b.kind, b.owner_user_id, b.name, b.sector_name, b.system_code, b.planet_number
		FROM bases b WHERE %s
		ORDER BY b.name, b.id LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2), pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var page []Base
	byID := make(map[uuid.UUID]*Base)
	var ids []uuid.UUID
	for rows.Next() {
		var b Base
		var owner *string
		if err := rows.Scan(&b.ID, &b.Kind, &owner, &b.Name, &b.SectorName, &b.SystemCode, &b.PlanetNumber); err != nil {
			return nil, 0, err
		}
		if owner != nil {
			b.OwnerUserID = *owner
		}
		page = append(page, b)
		ids = append(ids, b.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range page {
		byID[page[i].ID] = &page[i]
	}
	if err := r.attachEquipment(ctx, byID, ids); err != nil {
		return nil, 0, err
	}
	return page, total, nil
}

// attachEquipment loads the extractors and productions for a page of bases in
// two queries (not N+1) and attaches them to the matching Base.
func (r *pgRepository) attachEquipment(ctx context.Context, byID map[uuid.UUID]*Base, ids []uuid.UUID) error {
	erows, err := r.pool.Query(ctx,
		`SELECT bases_id, id, resource_name FROM base_extractors WHERE bases_id = ANY($1) ORDER BY created_at, id`, ids)
	if err != nil {
		return err
	}
	defer erows.Close()
	for erows.Next() {
		var baseID uuid.UUID
		var e Extractor
		if err := erows.Scan(&baseID, &e.ID, &e.ResourceName); err != nil {
			return err
		}
		if b := byID[baseID]; b != nil {
			b.Extractors = append(b.Extractors, e)
		}
	}
	if err := erows.Err(); err != nil {
		return err
	}

	prows, err := r.pool.Query(ctx,
		`SELECT bases_id, id, item_name FROM base_productions WHERE bases_id = ANY($1) ORDER BY created_at, id`, ids)
	if err != nil {
		return err
	}
	defer prows.Close()
	for prows.Next() {
		var baseID uuid.UUID
		var p Production
		if err := prows.Scan(&baseID, &p.ID, &p.ItemName); err != nil {
			return err
		}
		if b := byID[baseID]; b != nil {
			b.Productions = append(b.Productions, p)
		}
	}
	return prows.Err()
}
