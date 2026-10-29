package tools

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/render"
)

// listChannels lists the followed channels the read allowlist allows.
func listChannels(ctx context.Context, c *Call) (any, error) {
	r := c.Bridge.Channels(ctx)
	filterByScope(ctx, c, r, "channels", true)
	return r, nil
}

func getChannelMessages(ctx context.Context, c *Call) (any, error) {
	channelJID := c.Args.String("channel_jid")
	if hidden := c.DB.CheckReadable(ctx, channelJID); hidden != "" {
		return render.Refused(hidden), nil
	}
	return c.Bridge.ChannelMessages(ctx, channelJID, c.Args.Int("count"), c.Args.Int("before_server_id")), nil
}

func followChannel(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.FollowChannel(ctx, c.Args.String("channel"), c.Args.Bool("follow"))), nil
}
