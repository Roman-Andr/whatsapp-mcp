package tools

// Your chat list: archiving, pinning, muting, and WhatsApp Business labels.

import "context"

func manageChat(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return result(c.Bridge.ManageChat(ctx, a.String("chat_jid"), a.String("action"), a.OptFloat("mute_hours"))), nil
}

func listLabels(ctx context.Context, c *Call) (any, error) {
	return c.DB.ListLabels(ctx), nil
}

func labelChat(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return result(c.Bridge.LabelChat(ctx, a.String("chat_jid"), a.String("label"), a.Bool("labeled"))), nil
}

func editLabel(ctx context.Context, c *Call) (any, error) {
	a := c.Args
	return sendResult(c.Bridge.EditLabel(ctx, a.String("name"), a.String("label_id"), a.Int("color"), a.Bool("deleted"))), nil
}
