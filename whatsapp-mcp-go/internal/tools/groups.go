package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/render"
)

// getGroupInfo asks WhatsApp for a group's name, description, members with
// names and admin roles, and the invite link when you are an admin.
func getGroupInfo(ctx context.Context, c *Call) (any, error) {
	chatJID := c.Args.String("chat_jid")
	if chatJID == "" || !strings.HasSuffix(chatJID, "@g.us") {
		return render.Refused(fmt.Sprintf("%s is not a group JID (…@g.us)", pyfmt.Repr(chatJID))), nil
	}
	if hidden := c.DB.CheckReadable(ctx, chatJID); hidden != "" {
		return render.Refused(hidden), nil
	}
	return c.Bridge.GroupInfo(ctx, chatJID, c.Args.Bool("include_invite_link")), nil
}

func createGroup(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.CreateGroup(ctx, a.String("name"), a.StringSlice("participants"), a.Bool("community")), nil
}

func leaveGroup(ctx context.Context, c *Call) (any, error) {
	return c.Bridge.LeaveGroup(ctx, c.Args.String("chat_jid")), nil
}

func updateGroupParticipants(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.UpdateGroupParticipants(ctx, a.String("chat_jid"), a.String("action"), a.StringSlice("participants")), nil
}

func updateGroupSettings(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.UpdateGroupSettings(ctx, a.String("chat_jid"), bridgeapi.GroupSettings{
		Name: a.OptString("name"), Description: a.OptString("description"),
		AnnounceOnly: a.OptBool("announce_only"), Locked: a.OptBool("locked"), JoinApproval: a.OptBool("join_approval"),
	}), nil
}

func joinGroup(ctx context.Context, c *Call) (any, error) {
	return c.Bridge.JoinGroup(ctx, c.Args.String("invite")), nil
}

func getGroupJoinRequests(ctx context.Context, c *Call) (any, error) {
	chatJID := c.Args.String("chat_jid")
	if hidden := c.DB.CheckReadable(ctx, chatJID); hidden != "" {
		return render.Refused(hidden), nil
	}
	return c.Bridge.GroupJoinRequests(ctx, chatJID), nil
}

func updateGroupJoinRequests(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return c.Bridge.UpdateGroupJoinRequests(ctx, a.String("chat_jid"), a.String("action"), a.StringSlice("participants")), nil
}

func resetGroupInviteLink(ctx context.Context, c *Call) (any, error) {
	return c.Bridge.ResetGroupInviteLink(ctx, c.Args.String("chat_jid")), nil
}

func setDisappearingMessages(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.SetDisappearingMessages(ctx, c.Args.String("chat_jid"), c.Args.String("timer"))), nil
}
