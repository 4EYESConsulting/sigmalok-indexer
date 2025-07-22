package notifier

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
)

func BuildDiscordMessage(
	baseURI string,
	amountSent int,
	txHash string,
	currentBlockHeight uint64,
) discord.WebhookMessageCreate {
	embed := discord.NewEmbedBuilder().
		SetColor(0xd9dadb).
		SetAuthor("template", "", "https://i.imgur.com/1xbxWrX.png").
		SetTitle(fmt.Sprintf("%s ADA Sent", formatTokenAmount(amountSent))).
		SetURL(baseURI+txHash).
		SetThumbnail("https://i.imgur.com/XILpfxc.png").
		SetFooter(fmt.Sprintf("Block %d", currentBlockHeight), "https://i.imgur.com/rzmSALh.png")

	return discord.NewWebhookMessageCreateBuilder().
		SetUsername("4eyes").
		SetAvatarURL("https://pbs.twimg.com/profile_images/1770426683339755520/eRHY1Ex5_400x400.jpg").
		SetEmbeds(embed.Build()).
		Build()
}
