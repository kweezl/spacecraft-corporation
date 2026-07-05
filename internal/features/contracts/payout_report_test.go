package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
)

// TestReport_ViewOpensEphemeralContractView: the report's View button opens the
// console contract view as a fresh ephemeral (never editing the shared report),
// for a manager.
func TestReport_ViewOpensEphemeralContractView(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(completedConsoleProgress(cid), nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("chan", member("mgr"), "contract:repview:"+cid.String()), gid))
	require.NotEmpty(t, r.components)
	assert.False(t, r.updated, "a fresh ephemeral view, not an in-place edit of the report")
}

// TestReport_ViewDeniedForNonManager: a participant in the reports channel can't
// open the console view from the report.
func TestReport_ViewDeniedForNonManager(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t) // ProgressByIDScoped must NOT be called

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.use"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("chan", member("nobody"), "contract:repview:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "non-manager denied")
	assert.Empty(t, r.components)
}

// TestReport_MarkPaidEditsReportAndConfirms: the winning press records the paid
// state, edits the report in place, and confirms ephemerally.
func TestReport_MarkPaidEditsReportAndConfirms(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	repo.EXPECT().MarkPayoutsPaid(mock.Anything, gid, cid, "mgr", mock.Anything).Return(true, nil).Once()
	paid := completedConsoleProgress(cid)
	now := time.Now()
	paid.PayoutsPaidAt = &now
	paid.PayoutsPaidByUserID = "mgr"
	paid.PayoutReportChannelID = reportsChan
	paid.PayoutReportMessageID = "msg-1"
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(paid, nil).Once() // editReportAfterPaid
	repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
	gw.EXPECT().EditChannelMessage(reportsChan, "msg-1", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, gw, mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("chan", member("mgr"), "contract:reppaid:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "ephemeral confirmation")
	assert.False(t, r.updated, "the report edit is a gateway call, not an interaction update")
}

// TestReport_MarkPaidLoser: a concurrent second press loses the SQL guard and
// gets the already-paid notice (no report edit).
func TestReport_MarkPaidLoser(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().MarkPayoutsPaid(mock.Anything, gid, cid, "mgr", mock.Anything).Return(false, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("chan", member("mgr"), "contract:reppaid:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "already-paid notice")
}
