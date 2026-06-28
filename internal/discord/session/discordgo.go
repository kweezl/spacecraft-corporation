package session

import "github.com/bwmarrin/discordgo"

type discordSession struct {
	s *discordgo.Session
}

// NewFactory returns a Factory that builds real discordgo-backed sessions.
func NewFactory() Factory {
	return func(token string) (Discord, error) {
		s, err := discordgo.New("Bot " + token)
		if err != nil {
			return nil, err
		}
		s.Identify.Intents = discordgo.IntentsGuilds
		return &discordSession{s: s}, nil
	}
}

func (d *discordSession) AddInteractionHandler(fn func(*discordgo.InteractionCreate)) {
	d.s.AddHandler(func(_ *discordgo.Session, i *discordgo.InteractionCreate) { fn(i) })
}

func (d *discordSession) AddGuildCreateHandler(fn func(*discordgo.GuildCreate)) {
	d.s.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildCreate) { fn(e) })
}

func (d *discordSession) AddGuildDeleteHandler(fn func(*discordgo.GuildDelete)) {
	d.s.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildDelete) { fn(e) })
}

func (d *discordSession) Open() error  { return d.s.Open() }
func (d *discordSession) Close() error { return d.s.Close() }

// Connected reports discordgo's DataReady, set true once the gateway READY (or
// Resumed) event is processed and cleared on disconnect. Read under the
// session's lock, the same one discordgo takes when it mutates the flag.
func (d *discordSession) Connected() bool {
	d.s.RLock()
	defer d.s.RUnlock()
	return d.s.DataReady
}

func (d *discordSession) OverwriteCommands(serverID string, cmds []*discordgo.ApplicationCommand) error {
	_, err := d.s.ApplicationCommandBulkOverwrite(d.s.State.User.ID, serverID, cmds)
	return err
}

func (d *discordSession) Respond(i *discordgo.Interaction, content string) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}

func (d *discordSession) RespondEphemeral(i *discordgo.Interaction, content string) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (d *discordSession) RespondEmbed(i *discordgo.Interaction, embed *discordgo.MessageEmbed) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}},
	})
}

func (d *discordSession) RespondAutocomplete(i *discordgo.Interaction, choices []*discordgo.ApplicationCommandOptionChoice) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

func (d *discordSession) RespondEmbedComponents(i *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

// RespondEmbedComponentsEphemeral is RespondEmbedComponents with the ephemeral
// flag — the /contracts console, private to the invoking officer. UpdateMessage
// edits it in place on subsequent component/modal interactions (ephemerality is
// fixed at creation, so it is not re-set on update).
func (d *discordSession) RespondEmbedComponentsEphemeral(i *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// UpdateMessage edits the message the component is attached to (the pagination
// case): Discord replaces the embed and components in place rather than posting
// a new message.
func (d *discordSession) UpdateMessage(i *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

// RespondComponentsV2Ephemeral sends an ephemeral Components V2 message: the
// IsComponentsV2 flag means the body is components only (no content/embeds), so
// text is carried by TextDisplay components.
func (d *discordSession) RespondComponentsV2Ephemeral(i *discordgo.Interaction, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		},
	})
}

// UpdateComponentsV2 edits a Components V2 message in place. The IsComponentsV2
// flag must be kept on the update; ephemeral is fixed at creation, so it is not
// re-set here.
func (d *discordSession) UpdateComponentsV2(i *discordgo.Interaction, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: components,
			Flags:      discordgo.MessageFlagsIsComponentsV2,
		},
	})
}

// RespondModal opens a modal popup. The submit comes back as a separate
// InteractionModalSubmit carrying customID, which the registry routes by prefix.
// The IsComponentsV2 flag enables the modern modal layout (Label-wrapped inputs,
// and crucially select menus inside the modal), so a modal can gather both a
// choice and free text in one overlay.
func (d *discordSession) RespondModal(i *discordgo.Interaction, customID, title string, components []discordgo.MessageComponent) error {
	return d.s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID:   customID,
			Title:      title,
			Components: components,
			Flags:      discordgo.MessageFlagsIsComponentsV2,
		},
	})
}

func (d *discordSession) ForumThreadStartComplex(channelID string, threadData *discordgo.ThreadStart, messageData *discordgo.MessageSend) (*discordgo.Channel, error) {
	return d.s.ForumThreadStartComplex(channelID, threadData, messageData)
}

func (d *discordSession) ChannelMessageEditComplex(m *discordgo.MessageEdit) (*discordgo.Message, error) {
	return d.s.ChannelMessageEditComplex(m)
}

func (d *discordSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend) (*discordgo.Message, error) {
	return d.s.ChannelMessageSendComplex(channelID, data)
}

func (d *discordSession) ChannelEditComplex(channelID string, data *discordgo.ChannelEdit) (*discordgo.Channel, error) {
	return d.s.ChannelEditComplex(channelID, data)
}

func (d *discordSession) InteractionResponseEdit(i *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	return d.s.InteractionResponseEdit(i, edit)
}
