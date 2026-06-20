package bases

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

// componentPrefix namespaces this feature's component CustomIDs so the registry
// routes them here. CustomIDs are "base:list:<token>:<page>".
const componentPrefix = commandName

const (
	// pageTTL is how long a listing's paging context stays valid after the
	// command runs; a button click after this gets a "list expired" notice.
	pageTTL = 10 * time.Minute
	// pageCacheSize bounds how many live listings are paged at once (LRU).
	pageCacheSize = 1000
)

// pageQuery is the paging context behind a listing: the filter and server to
// re-run for each page. Kept server-side (keyed by a short token in the button
// CustomID) because component interactions don't carry the original options and
// CustomIDs are length-limited.
type pageQuery struct {
	serverID uuid.UUID
	filter   Filter
	created  time.Time
}

type pageStore struct {
	cache *lru.Cache[string, pageQuery]
}

func newPageStore() (*pageStore, error) {
	c, err := lru.New[string, pageQuery](pageCacheSize)
	if err != nil {
		return nil, fmt.Errorf("bases: new page store: %w", err)
	}
	return &pageStore{cache: c}, nil
}

func (s *pageStore) put(q pageQuery) (string, error) {
	token, err := uuidv7.New()
	if err != nil {
		return "", err
	}
	s.cache.Add(token, q)
	return token, nil
}

// get returns the paging context for a token, treating an expired one as absent.
func (s *pageStore) get(token string) (pageQuery, bool) {
	q, ok := s.cache.Get(token)
	if !ok {
		return pageQuery{}, false
	}
	if time.Since(q.created) > pageTTL {
		s.cache.Remove(token)
		return pageQuery{}, false
	}
	return q, true
}

// filterFrom builds the listing Filter from the list subcommand's options.
func filterFrom(opts []*discordgo.ApplicationCommandInteractionDataOption) Filter {
	return Filter{
		SectorName:  optString(opts, optSector),
		SystemCode:  optString(opts, optSystem),
		BaseName:    optString(opts, optName),
		Resource:    optString(opts, optResource),
		Item:        optString(opts, optItem),
		OwnerUserID: optString(opts, optMember),
	}
}

func (h *Feature) handleList(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	q := pageQuery{serverID: serverID, filter: filterFrom(opts), created: time.Now()}
	page, total, err := h.repo.List(ctx, serverID, q.filter, h.cfg.PageSize, 0)
	if err != nil {
		return fmt.Errorf("list bases: %w", err)
	}
	if total == 0 {
		embed := &discordgo.MessageEmbed{
			Title:       h.loc.Render(ctx, serverID, "bases.list.title", nil),
			Description: h.loc.Render(ctx, serverID, "bases.list.empty", nil),
		}
		return r.RespondEmbed(i.Interaction, embed)
	}
	token, err := h.pages.put(q)
	if err != nil {
		return fmt.Errorf("list bases: store page: %w", err)
	}
	embed, components := h.renderPage(ctx, serverID, page, 0, total, token)
	return r.RespondEmbedComponents(i.Interaction, embed, components)
}

// handleComponent answers a pagination button: re-run the stored query for the
// requested page and edit the message in place.
func (h *Feature) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	token, page, ok := parsePageCustomID(i.MessageComponentData().CustomID)
	if !ok {
		return fmt.Errorf("bases: bad component id %q", i.MessageComponentData().CustomID)
	}
	q, ok := h.pages.get(token)
	if !ok {
		expired := &discordgo.MessageEmbed{
			Title:       h.loc.Render(ctx, serverID, "bases.list.title", nil),
			Description: h.loc.Render(ctx, serverID, "bases.list.expired", nil),
		}
		return r.UpdateMessage(i.Interaction, expired, nil)
	}
	bases, total, err := h.repo.List(ctx, q.serverID, q.filter, h.cfg.PageSize, page*h.cfg.PageSize)
	if err != nil {
		return fmt.Errorf("list bases page: %w", err)
	}
	embed, components := h.renderPage(ctx, serverID, bases, page, total, token)
	return r.UpdateMessage(i.Interaction, embed, components)
}

// renderPage builds the embed and pagination buttons for one page. page is the
// zero-based page index that bases belongs to.
func (h *Feature) renderPage(ctx context.Context, serverID uuid.UUID, bases []Base, page, total int, token string) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	totalPages := (total + h.cfg.PageSize - 1) / h.cfg.PageSize

	fields := make([]*discordgo.MessageEmbedField, 0, len(bases))
	for _, b := range bases {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  baseLabel(b),
			Value: h.entryValue(ctx, serverID, b),
		})
	}
	embed := &discordgo.MessageEmbed{
		Title:  h.loc.Render(ctx, serverID, "bases.list.title", nil),
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: h.loc.Render(ctx, serverID, "bases.list.footer", map[string]any{
				"Page": page + 1, "Pages": totalPages, "Total": total,
			}),
		},
	}
	return embed, h.pageButtons(ctx, serverID, token, page, totalPages)
}

// entryValue renders one base's owner + equipment block (the field value).
func (h *Feature) entryValue(ctx context.Context, serverID uuid.UUID, b Base) string {
	owner := h.loc.Render(ctx, serverID, "bases.list.owner_corp", nil)
	if b.Kind == KindMember {
		owner = h.loc.Render(ctx, serverID, "bases.list.owner_member", map[string]any{"User": b.OwnerUserID})
	}
	none := h.loc.Render(ctx, serverID, "bases.list.none", nil)
	return h.loc.Render(ctx, serverID, "bases.list.entry", map[string]any{
		"Owner":       owner,
		"Extractors":  joinOr(extractorNames(b), none),
		"Productions": joinOr(productionNames(b), none),
	})
}

// pageButtons returns the prev/next row, or nil when there is only one page.
func (h *Feature) pageButtons(ctx context.Context, serverID uuid.UUID, token string, page, totalPages int) []discordgo.MessageComponent {
	if totalPages <= 1 {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "bases.list.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(token, page-1),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "bases.list.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(token, page+1),
			Disabled: page >= totalPages-1,
		},
	}}}
}

func pageCustomID(token string, page int) string {
	return fmt.Sprintf("%s:list:%s:%d", componentPrefix, token, page)
}

// parsePageCustomID is the inverse of pageCustomID.
func parsePageCustomID(id string) (token string, page int, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 4 || parts[0] != componentPrefix || parts[1] != "list" {
		return "", 0, false
	}
	page, err := strconv.Atoi(parts[3])
	if err != nil || page < 0 {
		return "", 0, false
	}
	return parts[2], page, true
}

func extractorNames(b Base) []string {
	out := make([]string, len(b.Extractors))
	for i, e := range b.Extractors {
		out[i] = e.ResourceName
	}
	return out
}

func productionNames(b Base) []string {
	out := make([]string, len(b.Productions))
	for i, p := range b.Productions {
		out[i] = p.ItemName
	}
	return out
}

// joinOr joins names with commas, or returns the empty placeholder when none.
func joinOr(names []string, empty string) string {
	if len(names) == 0 {
		return empty
	}
	return strings.Join(names, ", ")
}
