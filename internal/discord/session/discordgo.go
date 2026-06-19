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
