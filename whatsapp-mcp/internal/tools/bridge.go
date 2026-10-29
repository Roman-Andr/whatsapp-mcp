package tools

// The bridges themselves: their accounts, status and connection.

import (
	"context"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

// listAccounts reports the configured accounts and whether each bridge is
// reachable and linked.
func listAccounts(ctx context.Context, c *Call) (any, error) {
	accounts, err := c.deps.Accounts.List()
	if err != nil {
		return nil, err
	}
	out := []*pyfmt.OMap{}
	for _, name := range account.SortedNames(accounts) {
		a := accounts[name]
		status := c.deps.bridge(a).Status(ctx)
		errText := status.Get("error")
		if !pyfmt.Truthy(errText) {
			errText = status.Get("warning")
		}
		out = append(out, pyfmt.NewOMap("name", name, "default", name == c.deps.Accounts.DefaultName(), "store_dir", a.StoreDir,
			"api_url", a.APIURL, "connected", pyfmt.Truthy(status.Get("connected")), "jid", status.Get("jid"), "error", errText))
	}
	return out, nil
}

func getBridgeStatus(ctx context.Context, c *Call) (any, error) {
	return c.Bridge.Status(ctx), nil
}

func reconnectBridge(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.ReconnectBridge(ctx)), nil
}
