package contracts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/testdb"
)

const (
	u1     = "user-1"
	u2     = "user-2"
	mgr    = "officer-1"
	thread = "thread-1"
)

func ptrTime(t time.Time) *time.Time { return &t }

// dec / decPtr build decimals for test fixtures.
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

type contractsSuite struct {
	testdb.Suite
	repo Repository
}

func (s *contractsSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.repo = newRepository(s.Pool, outbox.NewEnqueuer())
}

func TestPgRepository(t *testing.T) { suite.Run(t, new(contractsSuite)) }

func (s *contractsSuite) seed() (Repository, context.Context, uuid.UUID) {
	g1 := testdb.SeedServer(s.T(), s.Pool, "g1")
	return s.repo, context.Background(), g1
}

// newContract creates an open contract with a far-future deadline and assigns it
// a thread, simulating the worker that normally creates the forum thread.
func (s *contractsSuite) newContract(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	return s.newContractDeadline(ctx, g, threadID, time.Now().Add(24*time.Hour))
}

// --- thread→id adapters: the console keys mutations by UUID, so these resolve a
// thread to its contract/item ids to keep the test bodies readable. ---

func (s *contractsSuite) cidOf(ctx context.Context, g uuid.UUID, threadID string) uuid.UUID {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	return p.ID
}

func (s *contractsSuite) itemID(ctx context.Context, g uuid.UUID, threadID, name string) uuid.UUID {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	for _, it := range p.Items {
		if strings.EqualFold(it.Name, name) {
			return it.ID
		}
	}
	s.T().Fatalf("item %q not found on %q", name, threadID)
	return uuid.Nil
}

func (s *contractsSuite) addItem(ctx context.Context, g uuid.UUID, threadID, name string, qty, maxItems int, actor string) error {
	return s.repo.AddItemByID(ctx, g, s.cidOf(ctx, g, threadID), name, "", "", nil, qty, maxItems, actor)
}

func (s *contractsSuite) cancel(ctx context.Context, g uuid.UUID, threadID, actor string) error {
	return s.repo.CancelByID(ctx, g, s.cidOf(ctx, g, threadID), actor)
}

func (s *contractsSuite) TestCreateAndProgress() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 500, 25, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Iron Plate", 1000, 25, mgr))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusOpen, p.Status)
	require.Len(t, p.Items, 2)
	assert.Equal(t, "Steel", p.Items[0].Name)
	assert.Equal(t, 500, p.Items[0].RequiredQty)
}

func (s *contractsSuite) TestPostVersion_StampedOnCreateAndSetThread() {
	t := s.T()
	repo, ctx, g := s.seed()
	id, err := repo.Create(ctx, CreateInput{ServerID: g, Kind: KindCustom, Title: "X", CreatedByUserID: mgr})
	require.NoError(t, err)
	p, err := repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, CurrentPostVersion, p.PostVersion, "create stamps the current post version")

	// SetThreadID re-stamps the current version, so a recreated post (which clears
	// the thread then re-creates) lands at CurrentPostVersion and isn't re-migrated.
	require.NoError(t, repo.SetThreadID(ctx, id, "thread-x"))
	p, err = repo.ProgressByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, CurrentPostVersion, p.PostVersion)
}

func (s *contractsSuite) TestProgress_Participants() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 200))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 50)
	require.NoError(t, err)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	require.Len(t, p.Items, 1)
	parts := p.Items[0].Participants
	require.Len(t, parts, 2)
	assert.Equal(t, Participant{UserID: u1, Reserved: 200, Delivered: 50}, parts[0])
	assert.Equal(t, Participant{UserID: u2, Reserved: 100, Delivered: 0}, parts[1])
}

func (s *contractsSuite) TestAddItem_DuplicateAndLimit() {
	t := s.T()
	_, ctx, g := s.seed()
	s.newContract(ctx, g, thread)

	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 500, 2, mgr))
	require.ErrorIs(t, s.addItem(ctx, g, thread, "steel", 10, 2, mgr), ErrItemExists)
	require.NoError(t, s.addItem(ctx, g, thread, "Iron", 10, 2, mgr))
	require.ErrorIs(t, s.addItem(ctx, g, thread, "Copper", 10, 2, mgr), ErrMaxItems)
}

func (s *contractsSuite) TestParticipate_CapAndAdditive() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))

	require.NoError(t, repo.Participate(ctx, g, thread, "steel", u1, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 10))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 800))
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u2, 91), ErrOverCap)
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 90))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 1000, p.Items[0].ReservedQty)
	assert.Equal(t, 0, p.Items[0].Remaining())
}

func (s *contractsSuite) TestDeliver_BoundsAndCompletion() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))

	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.ErrorIs(t, err, ErrNoReservation)

	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 60))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 40))

	_, err = repo.Deliver(ctx, g, thread, "Steel", u1, 61)
	require.ErrorIs(t, err, ErrOverReserved)

	complete, err := repo.Deliver(ctx, g, thread, "Steel", u1, 60)
	require.NoError(t, err)
	assert.False(t, complete)

	complete, err = repo.Deliver(ctx, g, thread, "Steel", u2, 40)
	require.NoError(t, err)
	assert.True(t, complete)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, p.Status)
	require.ErrorIs(t, repo.Participate(ctx, g, thread, "Steel", u1, 1), ErrClosed)
}

func (s *contractsSuite) TestRelease_FloorAtDelivered() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1000, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)

	require.ErrorIs(t, repo.Release(ctx, g, thread, "Steel", u1, 91, u1), ErrBelowDelivered)
	require.NoError(t, repo.Release(ctx, g, thread, "Steel", u1, 90, u1))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, 10, p.Items[0].ReservedQty)
	assert.Equal(t, 10, p.Items[0].DeliveredQty)
}

// --- console (id-keyed) repository methods ---

func (s *contractsSuite) TestDeliverByItem_BoundsAndCompletion() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 100))
	itemID := s.itemID(ctx, g, thread, "Steel")

	// Can't deliver more than reserved-minus-delivered.
	_, _, err := repo.DeliverByItem(ctx, g, itemID, u1, 101, mgr)
	require.ErrorIs(t, err, ErrOverReserved)

	// Delivering the full requirement of the only item completes the contract.
	cid, complete, err := repo.DeliverByItem(ctx, g, itemID, u1, 100, mgr)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, cid)
	assert.True(t, complete)
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, p.Status)
}

func (s *contractsSuite) TestSetReservationByItem_FloorAndCap() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)
	itemID := s.itemID(ctx, g, thread, "Steel")

	// Raise within the item's capacity.
	_, err = repo.SetReservationByItem(ctx, g, itemID, u1, 80, mgr)
	require.NoError(t, err)
	assert.Equal(t, 80, s.byName(ctx, g, thread, "Steel").ReservedQty)

	// Can't drop below what's already delivered, nor exceed capacity.
	_, err = repo.SetReservationByItem(ctx, g, itemID, u1, 5, mgr)
	require.ErrorIs(t, err, ErrBelowDelivered)
	_, err = repo.SetReservationByItem(ctx, g, itemID, u1, 120, mgr)
	require.ErrorIs(t, err, ErrOverCap)

	// Setting to the delivered amount keeps the delivered, drops the rest.
	_, err = repo.SetReservationByItem(ctx, g, itemID, u1, 10, mgr)
	require.NoError(t, err)
	it := s.byName(ctx, g, thread, "Steel")
	assert.Equal(t, 10, it.ReservedQty)
	assert.Equal(t, 10, it.DeliveredQty)
}

func (s *contractsSuite) TestRemoveReservation() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	_, err := repo.Deliver(ctx, g, thread, "Steel", u1, 10)
	require.NoError(t, err)

	itemID := s.itemID(ctx, g, thread, "Steel")
	_, err = repo.RemoveReservation(ctx, g, itemID, u1, mgr)
	require.NoError(t, err)
	// Removing a non-existent reservation is a no-op error.
	_, err = repo.RemoveReservation(ctx, g, itemID, u1, mgr)
	require.ErrorIs(t, err, ErrNoReservation)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Empty(t, p.Items[0].Participants)
}

func (s *contractsSuite) TestRemoveItemByID_CascadesReservations() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u1, 40))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel", u2, 30))

	itemID := s.itemID(ctx, g, thread, "Steel")
	cid, cleared, err := repo.RemoveItemByID(ctx, g, itemID, mgr)
	require.NoError(t, err)
	assert.Equal(t, 2, cleared)
	assert.Equal(t, s.cidOf(ctx, g, thread), cid)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Empty(t, p.Items)
}

func (s *contractsSuite) TestUpdateItem_NameQtyCollisionAndReserved() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Iron", 100, 25, mgr))

	steel := s.itemID(ctx, g, thread, "Steel")
	// A colliding name is rejected.
	_, err := repo.UpdateItem(ctx, g, steel, "iron", 100, mgr)
	require.ErrorIs(t, err, ErrItemExists)
	// Rename + re-quantify together.
	_, err = repo.UpdateItem(ctx, g, steel, "Titanium", 250, mgr)
	require.NoError(t, err)
	ti := s.byName(ctx, g, thread, "Titanium")
	assert.Equal(t, "Titanium", ti.Name)
	assert.Equal(t, 250, ti.RequiredQty)
	// A quantity below what is already reserved is refused.
	require.NoError(t, repo.Participate(ctx, g, thread, "Titanium", u1, 80))
	_, err = repo.UpdateItem(ctx, g, steel, "Titanium", 50, mgr)
	require.ErrorIs(t, err, ErrQtyBelowReserved)
}

func (s *contractsSuite) byName(ctx context.Context, g uuid.UUID, threadID, name string) Item {
	p, err := s.repo.Progress(ctx, g, threadID)
	require.NoError(s.T(), err)
	for _, it := range p.Items {
		if it.Name == name {
			return it
		}
	}
	s.T().Fatalf("item %q not found", name)
	return Item{}
}

func (s *contractsSuite) TestUpdateDetails() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)
	require.NoError(t, repo.UpdateDetails(ctx, g, cid, "New Title", "New desc", mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, "New Title", p.Title)
	assert.Equal(t, "New desc", p.Description)
}

func (s *contractsSuite) TestSetDeadline_ClearAndSet() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// Clear it: no deadline, never auto-expires.
	require.NoError(t, repo.SetDeadline(ctx, g, cid, nil, mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Nil(t, p.Deadline)
	due, err := repo.DueContracts(ctx, time.Now().Add(100*time.Hour), 10)
	require.NoError(t, err)
	assert.NotContains(t, due, cid, "a deadline-less contract is never due")

	// Set it back.
	require.NoError(t, repo.SetDeadline(ctx, g, cid, ptrTime(time.Now().Add(3*time.Hour)), mgr))
	p, err = repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	require.NotNil(t, p.Deadline)
}

func (s *contractsSuite) TestAddItem_GDID() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Steel Ingot", "SteelIngot", "v1", nil, 500, 25, mgr))

	// Same gdid under a different name snapshot -> ErrItemExists (catalog identity).
	require.ErrorIs(t, repo.AddItemByID(ctx, g, cid, "Стальной слиток", "SteelIngot", "v1", nil, 10, 25, mgr), ErrItemExists)
	// Same name snapshot under a different gdid -> ErrItemExists (panel identity).
	require.ErrorIs(t, repo.AddItemByID(ctx, g, cid, "steel ingot", "OtherItem", "v1", nil, 10, 25, mgr), ErrItemExists)
	// A legacy free-text item coexists with gdid items.
	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Handwritten", "", "", nil, 5, 25, mgr))

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	require.Len(t, p.Items, 2)
	assert.Equal(t, "SteelIngot", p.Items[0].GDID)
	assert.Equal(t, "v1", p.Items[0].GDVersion)
	assert.Equal(t, "Steel Ingot", p.Items[0].Name)
	assert.Empty(t, p.Items[1].GDID, "legacy item has no gdid")
	assert.Empty(t, p.Items[1].GDVersion)
}

// TestMemberOutstanding_CarriesGDID covers Update A: the deliver/release picker
// query returns each outstanding item's gamedata link (empty for a free-text
// item) so the op modal can show the catalog icon.
func (s *contractsSuite) TestMemberOutstanding_CarriesGDID() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Steel Ingot", "SteelIngot", "v1", nil, 500, 25, mgr))
	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Handwritten", "", "", nil, 50, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, thread, "Steel Ingot", u1, 100))
	require.NoError(t, repo.Participate(ctx, g, thread, "Handwritten", u1, 20))

	items, err := repo.MemberOutstanding(ctx, g, thread, u1)
	require.NoError(t, err)
	require.Len(t, items, 2)
	byName := map[string]MemberItem{}
	for _, m := range items {
		byName[m.Name] = m
	}
	assert.Equal(t, "SteelIngot", byName["Steel Ingot"].GDID)
	assert.Equal(t, "v1", byName["Steel Ingot"].GDVersion)
	assert.Empty(t, byName["Handwritten"].GDID, "a free-text item carries no gdid")
	assert.Empty(t, byName["Handwritten"].GDVersion)
}

func (s *contractsSuite) TestAddItem_AliasDuplicate() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// A pre-gamedata free-text item, typed in English.
	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Hydraulic Actuator", "", "", nil, 100, 25, mgr))

	// Adding the same game item with a DIFFERENT-language snapshot is caught by
	// the alias set (the handler passes the catalog names in every language).
	aliases := []string{"Actuator", "Hydraulic Actuator", "Hydraulikaktor"}
	require.ErrorIs(t,
		repo.AddItemByID(ctx, g, cid, "Гидравлический актуатор", "Actuator", "v1", aliases, 10, 25, mgr),
		ErrItemExists)

	// An unrelated item with disjoint aliases still goes in.
	require.NoError(t, repo.AddItemByID(ctx, g, cid, "Steel Ingot", "SteelIngot", "v1", []string{"SteelIngot", "Steel Ingot"}, 10, 25, mgr))
}

func (s *contractsSuite) TestLinkItemGDID() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// Two pre-gamedata free-text items.
	require.NoError(t, s.addItem(ctx, g, thread, "Actuator (typed)", 100, 25, mgr))
	require.NoError(t, s.addItem(ctx, g, thread, "Steel Ingot", 50, 25, mgr))
	itemID := s.itemID(ctx, g, thread, "Actuator (typed)")

	// Linking stamps the gdid pair, keeps the stored name (panel identity), and
	// enqueues a card refresh.
	got, err := repo.LinkItemGDID(ctx, g, itemID, "Actuator", "v1", []string{"Actuator", "Hydraulic Actuator"}, mgr)
	require.NoError(t, err)
	assert.Equal(t, cid, got)
	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	linked := p.Items[0]
	assert.Equal(t, "Actuator (typed)", linked.Name, "the stored name snapshot is untouched")
	assert.Equal(t, "Actuator", linked.GDID)
	assert.Equal(t, "v1", linked.GDVersion)
	var tasks int
	require.NoError(t, s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_tasks WHERE kind = $1 AND chronometric_id = $2`, taskRefresh, cid).Scan(&tasks))
	assert.GreaterOrEqual(t, tasks, 1, "linking refreshes the forum card")

	// Relinking the same item to a different gdid is allowed (fixing a wrong link).
	_, err = repo.LinkItemGDID(ctx, g, itemID, "OtherThing", "v1", []string{"OtherThing"}, mgr)
	require.NoError(t, err)

	// Linking the OTHER item to a gdid already on the contract → duplicate.
	otherID := s.itemID(ctx, g, thread, "Steel Ingot")
	_, err = repo.LinkItemGDID(ctx, g, otherID, "OtherThing", "v1", []string{"OtherThing"}, mgr)
	require.ErrorIs(t, err, ErrItemExists)
	// Linking it to a gdid whose alias matches the first item's name → duplicate.
	_, err = repo.LinkItemGDID(ctx, g, otherID, "Fresh", "v1", []string{"Fresh", "actuator (TYPED)"}, mgr)
	require.ErrorIs(t, err, ErrItemExists)

	// Cross-server / forged ids resolve to nothing; closed contracts refuse.
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	_, err = repo.LinkItemGDID(ctx, g2, itemID, "X", "v1", nil, mgr)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.cancel(ctx, g, thread, mgr))
	_, err = repo.LinkItemGDID(ctx, g, otherID, "X", "v1", nil, mgr)
	require.ErrorIs(t, err, ErrClosed)
}

func (s *contractsSuite) TestCreate_WithItemsRewardsAndTemplateLink() {
	t := s.T()
	repo, ctx, g := s.seed()
	tid, err := repo.(TemplateRepository).CreateTemplate(ctx, g, "Weekly Steel", "desc", decimal.Zero, mgr)
	require.NoError(t, err)

	rep, lic := 5, 2
	id, err := repo.Create(ctx, CreateInput{
		ServerID: g, Kind: KindTemplate, Title: "From Tpl", Description: "d",
		RewardCredits: decPtr("1250.50"), RewardReputation: &rep, RewardLicencePoints: &lic,
		ParticipantRewardFactor: dec("33.33"),
		LocationGDID:            "Station_Cairn", LocationGDVersion: "v1", TemplateID: &tid,
		Items: []CreateItemInput{
			{Name: "Steel Ingot", GDID: "SteelIngot", GDVersion: "v1", Qty: 500},
			{Name: "Copper Wire", GDID: "CopperWire", GDVersion: "v1", Qty: 100},
		},
		CreatedByUserID: mgr,
	})
	require.NoError(t, err)

	p, err := repo.ProgressByIDScoped(ctx, g, id)
	require.NoError(t, err)
	assert.Equal(t, KindTemplate, p.Kind)
	require.NotNil(t, p.RewardCredits)
	assert.True(t, p.RewardCredits.Equal(dec("1250.50")), "credits round-trip exactly as decimals, got %s", p.RewardCredits)
	require.NotNil(t, p.RewardReputation)
	assert.Equal(t, 5, *p.RewardReputation)
	require.NotNil(t, p.RewardLicencePoints)
	assert.Equal(t, 2, *p.RewardLicencePoints)
	assert.True(t, p.ParticipantRewardFactor.Equal(dec("33.33")), "factor round-trips, got %s", p.ParticipantRewardFactor)
	assert.Equal(t, "Station_Cairn", p.LocationGDID)
	assert.Equal(t, "v1", p.LocationGDVersion)
	require.NotNil(t, p.TemplateID)
	assert.Equal(t, tid, *p.TemplateID)
	require.Len(t, p.Items, 2)
	assert.Equal(t, "SteelIngot", p.Items[0].GDID)

	// Contract + items + exactly one create-thread task committed atomically.
	var tasks int
	require.NoError(t, s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_tasks WHERE kind = $1 AND chronometric_id = $2`,
		taskCreateThread, id).Scan(&tasks))
	assert.Equal(t, 1, tasks)

	// A plain custom create leaves everything unset.
	id2, err := repo.Create(ctx, CreateInput{ServerID: g, Kind: KindCustom, Title: "Plain", CreatedByUserID: mgr})
	require.NoError(t, err)
	p2, err := repo.ProgressByIDScoped(ctx, g, id2)
	require.NoError(t, err)
	assert.Nil(t, p2.RewardCredits)
	assert.Nil(t, p2.RewardReputation)
	assert.Nil(t, p2.RewardLicencePoints)
	assert.True(t, p2.ParticipantRewardFactor.IsZero())
	assert.Empty(t, p2.LocationGDID)
	assert.Nil(t, p2.TemplateID)
	assert.Empty(t, p2.Items)
}

func (s *contractsSuite) TestUpdateRewards() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	rep := 3
	require.NoError(t, repo.UpdateRewards(ctx, g, cid, decPtr("99.90"), dec("15.5"), &rep, nil, mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	require.NotNil(t, p.RewardCredits)
	assert.True(t, p.RewardCredits.Equal(dec("99.90")), "got %s", p.RewardCredits)
	assert.True(t, p.ParticipantRewardFactor.Equal(dec("15.5")), "factor updates with the rewards, got %s", p.ParticipantRewardFactor)
	require.NotNil(t, p.RewardReputation)
	assert.Equal(t, 3, *p.RewardReputation)
	assert.Nil(t, p.RewardLicencePoints)

	// Clearing: nil values null the columns; the factor drops to zero (NOT NULL).
	require.NoError(t, repo.UpdateRewards(ctx, g, cid, nil, decimal.Zero, nil, nil, mgr))
	p, err = repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Nil(t, p.RewardCredits)
	assert.Nil(t, p.RewardReputation)
	assert.True(t, p.ParticipantRewardFactor.IsZero())

	// Open-guard: a cancelled contract refuses the mutation.
	require.NoError(t, s.cancel(ctx, g, thread, mgr))
	require.ErrorIs(t, repo.UpdateRewards(ctx, g, cid, decPtr("1"), decimal.Zero, nil, nil, mgr), ErrClosed)
}

func (s *contractsSuite) TestSetDeliveryLocation() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	require.NoError(t, repo.SetDeliveryLocation(ctx, g, cid, "Station_Cairn", "v1", mgr))
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, "Station_Cairn", p.LocationGDID)
	assert.Equal(t, "v1", p.LocationGDVersion)

	// Clearing with an empty gdid drops the version too (the pair CHECK).
	require.NoError(t, repo.SetDeliveryLocation(ctx, g, cid, "", "v1", mgr))
	p, err = repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Empty(t, p.LocationGDID)
	assert.Empty(t, p.LocationGDVersion)

	require.NoError(t, s.cancel(ctx, g, thread, mgr))
	require.ErrorIs(t, repo.SetDeliveryLocation(ctx, g, cid, "X", "v1", mgr), ErrClosed)
}

func (s *contractsSuite) TestProgressScoped_ForgedIDs() {
	t := s.T()
	repo, ctx, g := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	cid := s.newContract(ctx, g, thread)
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 100, 25, mgr))
	itemID := s.itemID(ctx, g, thread, "Steel")

	// Wrong server / random id -> ErrNotFound (the zero-rows guarantee).
	_, err := repo.ProgressByIDScoped(ctx, g2, cid)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.ProgressByIDScoped(ctx, g, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.ProgressByItemScoped(ctx, g2, itemID)
	require.ErrorIs(t, err, ErrNotFound)

	// Right server resolves the whole contract from an item id.
	p, err := repo.ProgressByItemScoped(ctx, g, itemID)
	require.NoError(t, err)
	assert.Equal(t, cid, p.ID)
}

func (s *contractsSuite) TestExpiry_LazyGuardAndSweep() {
	t := s.T()
	repo, ctx, g := s.seed()
	id := s.newContractDeadline(ctx, g, thread, time.Now().Add(-time.Minute))

	// A past-deadline (still 'open') contract refuses console mutations.
	require.ErrorIs(t, s.addItem(ctx, g, thread, "Steel", 1, 25, mgr), ErrExpired)

	due, err := repo.DueContracts(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Contains(t, due, id)

	flipped, err := repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.True(t, flipped)
	flipped, err = repo.MarkExpired(ctx, id, time.Now())
	require.NoError(t, err)
	assert.False(t, flipped)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, p.Status)
}

func (s *contractsSuite) TestNoDeadline_NotExpired() {
	t := s.T()
	_, ctx, g := s.seed()
	// Create with no deadline; console mutations are allowed (never "expired").
	id, err := s.repo.Create(ctx, CreateInput{ServerID: g, Kind: KindCustom, Title: "Endless", CreatedByUserID: mgr})
	require.NoError(t, err)
	require.NoError(t, s.repo.SetThreadID(ctx, id, thread))
	require.NoError(t, s.addItem(ctx, g, thread, "Steel", 1, 25, mgr))

	due, err := s.repo.DueContracts(ctx, time.Now().Add(1000*time.Hour), 10)
	require.NoError(t, err)
	assert.NotContains(t, due, id)
}

func (s *contractsSuite) TestCancelByID() {
	t := s.T()
	repo, ctx, g := s.seed()
	s.newContract(ctx, g, thread)
	require.NoError(t, s.cancel(ctx, g, thread, mgr))
	require.ErrorIs(t, s.cancel(ctx, g, thread, mgr), ErrClosed)

	p, err := repo.Progress(ctx, g, thread)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, p.Status)
}

func (s *contractsSuite) TestRepublishAndClearThread() {
	t := s.T()
	repo, ctx, g := s.seed()
	cid := s.newContract(ctx, g, thread)

	// Thread set + open -> refresh.
	act, err := repo.Republish(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, RepublishRefreshing, act)

	// Simulate the post being deleted (thread cleared) -> republish recreates.
	_, err = s.Pool.Exec(ctx, `UPDATE contracts SET thread_id = NULL WHERE id = $1`, cid)
	require.NoError(t, err)
	p, err := repo.ProgressByIDScoped(ctx, g, cid)
	require.NoError(t, err)
	assert.Empty(t, p.ThreadID)
	act, err = repo.Republish(ctx, g, cid)
	require.NoError(t, err)
	assert.Equal(t, RepublishCreating, act)
}

func (s *contractsSuite) TestList_MultiStatusAndCounts() {
	t := s.T()
	repo, ctx, g := s.seed()

	for _, th := range []string{"t-a", "t-b", "t-c"} {
		s.newContract(ctx, g, th)
	}
	s.newContract(ctx, g, "t-x")
	require.NoError(t, s.cancel(ctx, g, "t-x", mgr))
	// A deadline-less contract sorts last but is still listed.
	endless, err := repo.Create(ctx, CreateInput{ServerID: g, Kind: KindCustom, Title: "Endless", CreatedByUserID: mgr})
	require.NoError(t, err)
	require.NoError(t, repo.SetThreadID(ctx, endless, "t-endless"))

	require.NoError(t, s.addItem(ctx, g, "t-a", "Steel", 100, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "t-a", "Steel", u1, 40))
	_, err = repo.Deliver(ctx, g, "t-a", "Steel", u1, 25)
	require.NoError(t, err)

	// Open filter: 3 originals + the deadline-less one = 4.
	page, total, err := repo.List(ctx, g, []Status{StatusOpen}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	require.Len(t, page, 4)

	// Multi-status open+cancelled = 5.
	_, total, err = repo.List(ctx, g, []Status{StatusOpen, StatusCancelled}, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, total)

	// Counts on t-a.
	var ta ListEntry
	for _, e := range page {
		if e.ThreadID == "t-a" {
			ta = e
		}
	}
	assert.Equal(t, 1, ta.ItemCount)
	assert.Equal(t, 1, ta.ParticipantCount)
	assert.Equal(t, 40, ta.TotalReserved)
	assert.Equal(t, 25, ta.TotalDelivered)
	assert.Equal(t, 100, ta.TotalRequired)
}

func (s *contractsSuite) TestCounts_ByLifecycleState() {
	t := s.T()
	repo, ctx, g := s.seed()

	// Two published open contracts (a thread → active).
	s.newContract(ctx, g, "t-a")
	s.newContract(ctx, g, "t-b")
	// One open contract with no thread yet (the worker hasn't posted it) → unpublished.
	_, err := repo.Create(ctx, CreateInput{ServerID: g, Kind: KindCustom, Title: "Pending", CreatedByUserID: mgr})
	require.NoError(t, err)
	// One cancelled (declined).
	s.newContract(ctx, g, "t-x")
	require.NoError(t, s.cancel(ctx, g, "t-x", mgr))
	// One driven to completion (finished) by delivering everything required.
	s.newContract(ctx, g, "t-done")
	require.NoError(t, s.addItem(ctx, g, "t-done", "Steel", 10, 25, mgr))
	require.NoError(t, repo.Participate(ctx, g, "t-done", "Steel", u1, 10))
	complete, err := repo.Deliver(ctx, g, "t-done", "Steel", u1, 10)
	require.NoError(t, err)
	require.True(t, complete)

	c, err := repo.Counts(ctx, g)
	require.NoError(t, err)
	assert.Equal(t, Counts{Active: 2, Unpublished: 1, Completed: 1, Cancelled: 1}, c)
}

func (s *contractsSuite) TestServerIsolation() {
	t := s.T()
	repo, ctx, g1 := s.seed()
	g2 := testdb.SeedServer(t, s.Pool, "g2")
	s.newContract(ctx, g1, thread)
	require.NoError(t, s.addItem(ctx, g1, thread, "Steel", 100, 25, mgr))

	_, err := repo.Progress(ctx, g2, thread)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, repo.Participate(ctx, g2, thread, "Steel", u1, 1), ErrNotFound)
}
