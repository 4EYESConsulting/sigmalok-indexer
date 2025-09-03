package notifier

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
)

func BuildNodeHealthAlert(
	consecutiveFailures int,
	maxFailures int,
	errorMessage string,
	responseTime int64,
	network string,
) discord.WebhookMessageCreate {
	embed := discord.NewEmbedBuilder().
		SetColor(0xff0000).
		SetAuthor("Sigmalok Indexer", "", "https://i.imgur.com/1xbxWrX.png").
		SetTitle("🚨 CRITICAL: Node Health Failure").
		SetDescription(fmt.Sprintf(
			"Node health check has failed **%d/%d** consecutive times.\n\n"+
				"**Error:** %s\n"+
				"**Response Time:** %dms\n"+
				"**Network:** %s",
			consecutiveFailures,
			maxFailures,
			errorMessage,
			responseTime,
			network,
		)).
		SetTimestamp(time.Now()).
		SetFooter("Sigmalok Health Monitor", "https://i.imgur.com/rzmSALh.png")

	return discord.NewWebhookMessageCreateBuilder().
		SetUsername("Sigmalok Monitor").
		SetAvatarURL("https://pbs.twimg.com/profile_images/1770426683339755520/eRHY1Ex5_400x400.jpg").
		SetEmbeds(embed.Build()).
		Build()
}

func BuildHeightStagnationAlert(
	currentHeight uint64,
	stagnationDuration time.Duration,
	limit time.Duration,
	network string,
) discord.WebhookMessageCreate {
	embed := discord.NewEmbedBuilder().
		SetColor(0xff6600).
		SetAuthor("Sigmalok Indexer", "", "https://i.imgur.com/1xbxWrX.png").
		SetTitle("⚠️ CRITICAL: Node Height Stagnation").
		SetDescription(fmt.Sprintf(
			"Node height has not increased for **%s** (limit: %s).\n\n"+
				"**Current Height:** %d\n"+
				"**Stagnation Duration:** %s\n"+
				"**Network:** %s\n\n"+
				"This may indicate the node is not syncing properly with the blockchain.",
			stagnationDuration.Round(time.Second), limit,
			currentHeight, stagnationDuration.Round(time.Second), network)).
		SetTimestamp(time.Now()).
		SetFooter("Sigmalok Health Monitor", "https://i.imgur.com/rzmSALh.png")

	return discord.NewWebhookMessageCreateBuilder().
		SetUsername("Sigmalok Monitor").
		SetAvatarURL("https://pbs.twimg.com/profile_images/1770426683339755520/eRHY1Ex5_400x400.jpg").
		SetEmbeds(embed.Build()).
		Build()
}
