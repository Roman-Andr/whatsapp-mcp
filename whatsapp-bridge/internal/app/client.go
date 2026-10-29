package app

import (
	"context"
	"log/slog"

	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// client is the WhatsApp client as the application drives it: what the
// bridge uses (wa.Client) plus the lifecycle, which wa.Client leaves out on
// purpose. Tests replace it through hooks.newClient.
type client interface {
	wa.Client
	Connect() error
	Disconnect()
	GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType whatsmeow.PairClientType, clientDisplayName string) (string, error)
	AddEventHandler(handler whatsmeow.EventHandler) uint32
}

// liveClient is a real whatsmeow client.
type liveClient struct {
	wa.Client
	wm *whatsmeow.Client
}

// newLiveClient builds the whatsmeow client for device, logging through
// log as module "Client".
func newLiveClient(device *wastore.Device, log *slog.Logger) client {
	wm := whatsmeow.NewClient(device, logging.WA(log, "Client"))
	// Messages that fail to decrypt are also requested from our own phone,
	// which can resend what a business sender's device never does.
	wm.AutomaticMessageRerequestFromPhone = true
	return liveClient{Client: wa.New(wm), wm: wm}
}

func (c liveClient) Connect() error { return c.wm.Connect() }
func (c liveClient) Disconnect()    { c.wm.Disconnect() }

func (c liveClient) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return c.wm.GetQRChannel(ctx)
}

func (c liveClient) PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType whatsmeow.PairClientType, clientDisplayName string) (string, error) {
	return c.wm.PairPhone(ctx, phone, showPushNotification, clientType, clientDisplayName)
}

func (c liveClient) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	return c.wm.AddEventHandler(handler)
}
