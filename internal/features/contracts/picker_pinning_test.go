package contracts

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// These tests pin the EXACT CustomID strings the gamedata picker/browser emit,
// byte-for-byte. Live forum panels and open console messages carry these ids;
// the gamepick extraction must keep every emitted/parsed id identical, so this
// table is the safety net — it is written before the refactor (green now) and
// must stay green after it, with no literal changed.
//
// The fixed target id and gdid below make the expected strings concrete.

var pinTarget = uuid.MustParse("00000000-0000-7000-8000-000000000001")

const pinGDID = "IronOre"

func TestPinning_PickBrowseCustomIDs(t *testing.T) {
	tid := pinTarget.String()
	cases := []struct {
		name string
		got  string
		want string
	}{
		// pick select, per destination.
		{"pick/ci", buildID(segPick, string(pickContractItem), tid), "contract:pick:ci:" + tid},
		{"pick/ti", buildID(segPick, string(pickTemplateItem), tid), "contract:pick:ti:" + tid},
		{"pick/il", buildID(segPick, string(pickItemLink), tid), "contract:pick:il:" + tid},
		// category browser.
		{"brw/ci", buildID(segBrowse, string(pickContractItem), tid), "contract:brw:ci:" + tid},
		{"brw/ti", buildID(segBrowse, string(pickTemplateItem), tid), "contract:brw:ti:" + tid},
		// category page (dest:target:cat:page:sub).
		{"brwi/ci", buildID(segBrowseItems, string(pickContractItem), tid, "Ore", intStr(2), "Metals"),
			"contract:brwi:ci:" + tid + ":Ore:2:Metals"},
		{"brwi/ci empty sub", buildID(segBrowseItems, string(pickContractItem), tid, "Ore", intStr(0), ""),
			"contract:brwi:ci:" + tid + ":Ore:0:"},
		// subcategory filter (dest:target:cat).
		{"brwsub/ti", buildID(segBrowseSub, string(pickTemplateItem), tid, "Ore"),
			"contract:brwsub:ti:" + tid + ":Ore"},
		// browser search opener.
		{"brws/ci", buildID(segBrowseSearch, string(pickContractItem), tid), "contract:brws:ci:" + tid},
		// qty modal submit (dest:target:gdid).
		{"m_bqty/ci", buildID(segMBrowseQty, string(pickContractItem), tid, pinGDID),
			"contract:m_bqty:ci:" + tid + ":" + pinGDID},
		{"m_bqty/ti", buildID(segMBrowseQty, string(pickTemplateItem), tid, pinGDID),
			"contract:m_bqty:ti:" + tid + ":" + pinGDID},
		// location picker + clear.
		{"lbrw/cl", buildID(segLocBrowse, string(pickContractLoc), tid), "contract:lbrw:cl:" + tid},
		{"lbrw/tl", buildID(segLocBrowse, string(pickTemplateLoc), tid), "contract:lbrw:tl:" + tid},
		{"lclr/cl", buildID(segLocClear, string(pickContractLoc), tid), "contract:lclr:cl:" + tid},
		{"lclr/tl", buildID(segLocClear, string(pickTemplateLoc), tid), "contract:lclr:tl:" + tid},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, c.got, "%s", c.name)
	}
}

func TestPinning_PickBackID(t *testing.T) {
	tid := pinTarget.String()
	assert.Equal(t, "contract:irow:"+tid, pickBackID(pickItemLink, pinTarget), "il → item row")
	assert.Equal(t, "contract:tview:"+tid+":0", pickBackID(pickTemplateItem, pinTarget), "ti → template view page 0")
	assert.Equal(t, "contract:tview:"+tid+":0", pickBackID(pickTemplateLoc, pinTarget), "tl → template view page 0")
	assert.Equal(t, "contract:view:"+tid, pickBackID(pickContractItem, pinTarget), "ci → contract view")
	assert.Equal(t, "contract:view:"+tid, pickBackID(pickContractLoc, pinTarget), "cl → contract view")
}

func TestPinning_PickSearchID(t *testing.T) {
	tid := pinTarget.String()
	assert.Equal(t, "contract:ilink:"+tid, pickSearchID(pickItemLink, pinTarget), "il → link modal")
	assert.Equal(t, "contract:brws:ci:"+tid, pickSearchID(pickContractItem, pinTarget), "ci → browser search")
	assert.Equal(t, "contract:brws:ti:"+tid, pickSearchID(pickTemplateItem, pinTarget), "ti → browser search")
}
