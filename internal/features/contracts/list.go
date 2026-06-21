package contracts

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
// routes them here. CustomIDs are "contract:list:<token>:<page>".
const componentPrefix = commandName

const (
	pageTTL       = 10 * time.Minute
	pageCacheSize = 1000
)

// pageQuery is the paging context behind a listing (the status filter + server),
// kept server-side and keyed by a short token in the button CustomID.
type pageQuery struct {
	serverID uuid.UUID
	status   string
	created  time.Time
}

type pageStore struct {
	cache *lru.Cache[string, pageQuery]
}

func newPageStore() (*pageStore, error) {
	c, err := lru.New[string, pageQuery](pageCacheSize)
	if err != nil {
		return nil, fmt.Errorf("contracts: new page store: %w", err)
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

func (h *Feature) handleList(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	q := pageQuery{serverID: serverID, status: optString(opts, optStatus), created: time.Now()}
	page, total, err := h.repo.List(ctx, serverID, q.status, h.cfg.PageSize, 0)
	if err != nil {
		return fmt.Errorf("list contracts: %w", err)
	}
	if total == 0 {
		embed := &discordgo.MessageEmbed{
			Title:       h.loc.Render(ctx, serverID, "contracts.list.title", nil),
			Description: h.loc.Render(ctx, serverID, "contracts.list.empty", nil),
		}
		return r.RespondEmbed(i.Interaction, embed)
	}
	token, err := h.pages.put(q)
	if err != nil {
		return fmt.Errorf("list contracts: store page: %w", err)
	}
	embed, components := h.renderPage(ctx, serverID, page, 0, total, token)
	return r.RespondEmbedComponents(i.Interaction, embed, components)
}

func (h *Feature) handleListComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	token, page, ok := parsePageCustomID(i.MessageComponentData().CustomID)
	if !ok {
		return fmt.Errorf("contracts: bad component id %q", i.MessageComponentData().CustomID)
	}
	q, ok := h.pages.get(token)
	if !ok {
		expired := &discordgo.MessageEmbed{
			Title:       h.loc.Render(ctx, serverID, "contracts.list.title", nil),
			Description: h.loc.Render(ctx, serverID, "contracts.list.expired", nil),
		}
		return r.UpdateMessage(i.Interaction, expired, nil)
	}
	page2, total, err := h.repo.List(ctx, q.serverID, q.status, h.cfg.PageSize, page*h.cfg.PageSize)
	if err != nil {
		return fmt.Errorf("list contracts page: %w", err)
	}
	embed, components := h.renderPage(ctx, serverID, page2, page, total, token)
	return r.UpdateMessage(i.Interaction, embed, components)
}

func (h *Feature) renderPage(ctx context.Context, serverID uuid.UUID, entries []ListEntry, page, total int, token string) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	totalPages := (total + h.cfg.PageSize - 1) / h.cfg.PageSize
	fields := make([]*discordgo.MessageEmbedField, 0, len(entries))
	for _, e := range entries {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   truncate(e.Title, 256),
			Value:  h.entryValue(ctx, serverID, e),
			Inline: false,
		})
	}
	embed := &discordgo.MessageEmbed{
		Title:  h.loc.Render(ctx, serverID, "contracts.list.title", nil),
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: h.loc.Render(ctx, serverID, "contracts.list.footer", map[string]any{
				"Page": page + 1, "Pages": totalPages, "Total": total,
			}),
		},
	}
	return embed, h.pageButtons(ctx, serverID, token, page, totalPages)
}

// entryValue renders one contract row: a clickable link to its thread (a
// <#channel> mention, which needs no guild id), its status/time-left, item
// count, and the overall delivered/required roll-up.
func (h *Feature) entryValue(ctx context.Context, serverID uuid.UUID, e ListEntry) string {
	status := h.statusLine(ctx, serverID, Progress{Contract: e.Contract})
	return h.loc.Render(ctx, serverID, "contracts.list.entry", map[string]any{
		"Thread":    e.ThreadID,
		"Status":    status,
		"Items":     e.ItemCount,
		"Delivered": e.TotalDelivered,
		"Required":  e.TotalRequired,
	})
}

func (h *Feature) pageButtons(ctx context.Context, serverID uuid.UUID, token string, page, totalPages int) []discordgo.MessageComponent {
	if totalPages <= 1 {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.list.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(token, page-1),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.list.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(token, page+1),
			Disabled: page >= totalPages-1,
		},
	}}}
}

func pageCustomID(token string, page int) string {
	return fmt.Sprintf("%s:list:%s:%d", componentPrefix, token, page)
}

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
