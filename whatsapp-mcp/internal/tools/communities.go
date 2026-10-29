package tools

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
)

func linkCommunityGroup(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.LinkCommunityGroup(ctx, a.String("community_jid"), a.String("group_jid"), a.Bool("unlink")), nil
}

func listCommunityGroups(ctx context.Context, c *Call) (any, error) {
	communityJID := c.Args.String("community_jid")
	if hidden := c.DB.CheckReadable(ctx, communityJID); hidden != "" {
		return render.Refused(hidden), nil
	}
	return c.Bridge.CommunityGroups(ctx, communityJID), nil
}
