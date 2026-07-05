package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
)

// TestConsole_ListFilterRemembered: a non-default list filter rides the Open
// button into the contract view and back out via Back, so drilling in and
// returning keeps the filter the user chose (mask 2 = completed).
func TestConsole_ListFilterRemembered(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))

	// Filter the list to "completed" via the multi-select.
	repo.EXPECT().List(mock.Anything, gid, mock.MatchedBy(func(ss []contracts.Status) bool {
		return len(ss) == 1 && ss[0] == contracts.StatusCompleted
	}), 3, 0).Return([]contracts.ListEntry{
		{Contract: contracts.Contract{ID: cid, ServerID: gid, ThreadID: "t9", Title: "Done", Status: contracts.StatusCompleted, LastRefreshedAt: time.Now()}},
	}, 1, nil).Once()

	r := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:cfilter", "completed"), gid))
	openID := "contract:view:" + cid.String() + ":2:0"
	assert.True(t, has(buttonIDs(r.components), openID), "the Open button carries the active filter (mask 2, page 0)")

	// Drilling in: the contract view's Back carries the same filter.
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusCompleted, LastRefreshedAt: time.Now()},
	}, nil).Once()
	r2 := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r2, component("", member("officer"), openID), gid))
	assert.True(t, has(buttonIDs(r2.components), "contract:cback:2:0"), "Back returns to the same filter")

	// Pressing Back re-renders the list at that filter (completed), not the default.
	repo.EXPECT().List(mock.Anything, gid, mock.MatchedBy(func(ss []contracts.Status) bool {
		return len(ss) == 1 && ss[0] == contracts.StatusCompleted
	}), 3, 0).Return(nil, 0, nil).Once()
	r3 := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r3, component("", member("officer"), "contract:cback:2:0"), gid))
	require.NotEmpty(t, r3.components)
}
