package contracts_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
)

// TestPickApply_ContractItem_UsesPerServerCap drives a real item pick to its
// apply through the shared m_bqty quantity modal, proving the contract-item
// destination resolves the per-server item cap (ItemCap) at apply time and
// passes it to AddItemByID. "Actuator" is a real, non-excluded catalog item, so
// it survives the picker's hard exclusion boundary.
func TestPickApply_ContractItem_UsesPerServerCap(t *testing.T) {
	cid := uuid.New()
	m := member("mgr")

	// Cap resolved to 2 from the ItemCap; AddItemByID must receive 2.
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().AddItemByID(mock.Anything, gid, cid, mock.Anything, "Actuator", mock.Anything, mock.Anything, 7, 2, mock.Anything).
		Return(nil).Once()
	// Success re-renders the contract view.
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).
		Return(contracts.Progress{Contract: contracts.Contract{ID: cid, Status: contracts.StatusOpen}}, nil).Once()

	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t),
		featureDeps{access: grant("contracts.manage"), itemCap: staticItemCap{limit: 2, set: true}})

	r := &capture{}
	dispatch(t, f, r, modalInputs(m, "contract:m_bqty:ci:"+cid.String()+":Actuator", map[string]string{"qty": "7"}))
}

// TestPickApply_ContractItem_MaxItemsShowsResolvedLimit proves ErrMaxItems is
// reported with the resolved per-server limit in the message.
func TestPickApply_ContractItem_MaxItemsShowsResolvedLimit(t *testing.T) {
	cid := uuid.New()
	m := member("mgr")

	repo := mocks.NewMockRepository(t)
	repo.EXPECT().AddItemByID(mock.Anything, gid, cid, mock.Anything, "Actuator", mock.Anything, mock.Anything, 7, 2, mock.Anything).
		Return(contracts.ErrMaxItems).Once()
	// No ProgressByIDScoped — the apply short-circuits with the max-items reply.

	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t),
		featureDeps{access: grant("contracts.manage"), itemCap: staticItemCap{limit: 2, set: true}})

	r := &capture{}
	dispatch(t, f, r, modalInputs(m, "contract:m_bqty:ci:"+cid.String()+":Actuator", map[string]string{"qty": "7"}))
	assert.Contains(t, r.content, "2", "the max-items notice carries the resolved per-server limit")
}

// TestPickApply_UnauthorizedDenied proves the apply is gated by the manager key:
// without it, AddItemByID is never called.
func TestPickApply_UnauthorizedDenied(t *testing.T) {
	cid := uuid.New()
	m := member("nobody")
	repo := mocks.NewMockRepository(t) // no AddItemByID expected

	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t),
		featureDeps{access: grant(), itemCap: staticItemCap{limit: 2, set: true}})

	r := &capture{}
	dispatch(t, f, r, modalInputs(m, "contract:m_bqty:ci:"+cid.String()+":Actuator", map[string]string{"qty": "7"}))
	require.NotEmpty(t, r.content, "denied reply rendered; no mutation attempted")
}
