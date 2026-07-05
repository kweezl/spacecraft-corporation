package gamedata

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

type ContractSuite struct {
	suite.Suite
	cat *Catalog
}

func TestContractSuite(t *testing.T) {
	suite.Run(t, new(ContractSuite))
}

// SetupTest builds a single-layer catalog with one fully-specified contract
// ("Haul") and one that pays no currency ("Barter"), mirroring the shape of the
// generated data: tangible items plus the three corporation currencies as
// reward lines.
func (s *ContractSuite) SetupTest() {
	src := source{
		version: "v1",
		items: map[schema.GDID]schema.Item{
			"Cartridge_Iron":  {ID: "Cartridge_Iron", IconName: "Cartridge_Iron"},
			"IronOre":         {ID: "IronOre", IconName: "IronOre"},
			"Beam1":           {ID: "Beam1", IconName: "Beam1"},
			"CorpoCredits":    {ID: "CorpoCredits"},
			"CorpoReputation": {ID: "CorpoReputation"},
			"LicensePoints":   {ID: "LicensePoints"},
		},
		names: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {
				"Cartridge_Iron": "Iron Cartridge",
				"IronOre":        "Iron Ore",
				"Beam1":          "Beam",
			},
			i18n.LanguageRU: {"IronOre": "Железная руда"},
		},
		descs:      map[i18n.Language]map[schema.GDID]string{},
		categories: map[schema.GDID]schema.Category{},
		contracts: map[schema.GDID]schema.Contract{
			"Haul": {
				ID:     "Haul",
				Client: "Stellar",
				Items: []schema.RequestItem{
					{Item: "Cartridge_Iron", Qty: 3600000},
					{Item: "IronOre", Qty: 50},
					{Item: "Ghost", Qty: 5}, // unknown id -> skipped
				},
				Rewards: []schema.RewardItem{
					{Item: "Beam1", Count: 10},
					{Item: "CorpoCredits", Count: 54000},
					{Item: "LicensePoints", Count: 1000},
					{Item: "CorpoReputation", Count: 12},
				},
			},
			"Barter": {
				ID:      "Barter",
				Items:   []schema.RequestItem{{Item: "IronOre", Qty: 5}},
				Rewards: []schema.RewardItem{{Item: "Beam1", Count: 1}},
			},
		},
		spaceObjects:  map[schema.GDID]schema.SpaceObject{},
		categoryNames: map[i18n.Language]map[schema.GDID]string{},
	}
	s.cat = newCatalog(src, nil)
}

func (s *ContractSuite) view(id schema.GDID, lang i18n.Language) ContractView {
	v, ok := s.cat.ContractView(id, lang)
	s.Require().True(ok)
	return v
}

func (s *ContractSuite) TestContractViewUnknown() {
	_, ok := s.cat.ContractView("Nope", i18n.LanguageEN)
	s.False(ok)
}

func (s *ContractSuite) TestRequiredItems() {
	got := s.view("Haul", i18n.LanguageEN).RequiredItems()

	// Unknown "Ghost" is dropped; order follows the contract's list.
	s.Require().Len(got, 2)

	s.Equal(schema.GDID("Cartridge_Iron"), got[0].ID)
	s.Equal("Iron Cartridge", got[0].Name)
	s.Equal("Cartridge_Iron", got[0].IconName)
	s.Equal(3600000, got[0].Qty)

	s.Equal(schema.GDID("IronOre"), got[1].ID)
	s.Equal("Iron Ore", got[1].Name)
	s.Equal(50, got[1].Qty)
}

func (s *ContractSuite) TestRequiredItemsLocalized() {
	got := s.view("Haul", i18n.LanguageRU).RequiredItems()
	s.Require().Len(got, 2)
	// IronOre has a Russian name...
	s.Equal("Железная руда", got[1].Name)
	// ...Cartridge_Iron does not, so it falls back to the default (en).
	s.Equal("Iron Cartridge", got[0].Name)
}

func (s *ContractSuite) TestRewardItemsExcludesCurrencies() {
	got := s.view("Haul", i18n.LanguageEN).RewardItems()

	// Only the tangible Beam1 reward; the three currencies are excluded.
	s.Require().Len(got, 1)
	s.Equal(schema.GDID("Beam1"), got[0].ID)
	s.Equal("Beam", got[0].Name)
	s.Equal("Beam1", got[0].IconName)
	s.Equal(10, got[0].Qty)
}

func (s *ContractSuite) TestRewardCorpoCredits() {
	v := s.view("Haul", i18n.LanguageEN)
	s.Equal(54000, v.RewardCorpoCredits(0))    // no bonus
	s.Equal(64800, v.RewardCorpoCredits(0.2))  // +20%
	s.Equal(54000, v.RewardCorpoCredits(-0.5)) // negative clamped to 0
}

func (s *ContractSuite) TestRewardCorpoReputationRounds() {
	v := s.view("Haul", i18n.LanguageEN)
	s.Equal(12, v.RewardCorpoReputation(0))
	// 12 × 1.15 = 13.8 -> rounds to 14.
	s.Equal(14, v.RewardCorpoReputation(0.15))
}

func (s *ContractSuite) TestRewardCorpoLicensePoints() {
	v := s.view("Haul", i18n.LanguageEN)
	s.Equal(1000, v.RewardCorpoLicensePoints(0))
	s.Equal(1200, v.RewardCorpoLicensePoints(0.2))
}

func (s *ContractSuite) TestRewardCurrencyAbsentIsZero() {
	v := s.view("Barter", i18n.LanguageEN)
	s.Equal(0, v.RewardCorpoCredits(0.2))
	s.Equal(0, v.RewardCorpoReputation(0.2))
	s.Equal(0, v.RewardCorpoLicensePoints(0.2))
}

func (s *ContractSuite) TestContractsEnumeratesSorted() {
	got := s.cat.Contracts()
	ids := make([]schema.GDID, len(got))
	for i, ct := range got {
		ids[i] = ct.ID
	}
	// Every template, sorted by id, so a picker can index them.
	s.Equal([]schema.GDID{"Barter", "Haul"}, ids)
}
