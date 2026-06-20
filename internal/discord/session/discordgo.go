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

func (d *discordSession) CreateCommand(serverID string, cmd *discordgo.ApplicationCommand) error {
	_, err := d.s.ApplicationCommandCreate(d.s.State.User.ID, serverID, cmd)
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
