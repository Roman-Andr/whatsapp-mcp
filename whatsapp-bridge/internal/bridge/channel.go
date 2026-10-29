package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

func describeChannel(m *types.NewsletterMetadata) Channel {
	c := Channel{
		JID: m.ID.String(), Name: m.ThreadMeta.Name.Text, Description: m.ThreadMeta.Description.Text,
		Subscribers: m.ThreadMeta.SubscriberCount, Verified: m.ThreadMeta.VerificationState == types.NewsletterVerificationStateVerified,
	}
	if code := m.ThreadMeta.InviteCode; code != "" {
		c.InviteLink = "https://whatsapp.com/channel/" + code
	}
	if m.ViewerMeta != nil {
		c.Role = string(m.ViewerMeta.Role)
		c.Muted = m.ViewerMeta.Mute == types.NewsletterMuteOn
	}
	return c
}

// resolveChannel accepts a channel JID, a whatsapp.com/channel/ link or its code.
func (b *Bridge) resolveChannel(ctx context.Context, channel string) (*types.NewsletterMetadata, error) {
	channel = strings.TrimSpace(channel)
	if strings.HasSuffix(channel, "@"+types.NewsletterServer) {
		jid, err := types.ParseJID(channel)
		if err != nil {
			return nil, err
		}
		return b.client.GetNewsletterInfo(ctx, jid)
	}
	code := channel
	if i := strings.Index(code, "/channel/"); i >= 0 {
		code = code[i+len("/channel/"):]
	}
	code, _, _ = strings.Cut(code, "?")
	code = strings.Trim(code, "/ ")
	if code == "" || strings.ContainsAny(code, "/ @") {
		return nil, fmt.Errorf("channel must be a …@newsletter JID, a whatsapp.com/channel/ link or its code")
	}
	return b.client.GetNewsletterInfoWithInvite(ctx, code)
}

// subscribedNewslettersQueryID is WhatsApp Web's current persisted query for
// the followed channels (WAWebMexFetchAllNewslettersMetadataJobQuery). The
// server rejects the 2023 query that whatsmeow's GetSubscribedNewsletters still
// sends with "graphql error: 400 Bad Request (CRITICAL)".
const subscribedNewslettersQueryID = "25399611239711790"

// subscribedNewslettersVariables are the query's variables, in the order
// WhatsApp Web's WAWebMexFetchAllNewslettersMetadataJob writes them. The server
// binds a persisted query's variables by name and answers a bare 400 Bad
// Request when a declared one is missing or null; false is accepted. Both gate
// response blocks the bridge does not read.
type subscribedNewslettersVariables struct {
	FetchWamoSub        bool `json:"fetch_wamo_sub"`
	FetchStatusMetadata bool `json:"fetch_status_metadata"`
}

// subscribedNewslettersPayload is the query's JSON body.
func subscribedNewslettersPayload() []byte {
	payload, _ := json.Marshal(struct {
		Variables subscribedNewslettersVariables `json:"variables"`
	}{})
	return payload
}

// parseSubscribedNewsletters reads the GraphQL result of the query. It keeps
// the channels the server did return when it also reports errors for some of
// them, and fails only when nothing usable came back. The error then carries
// the server's errors array verbatim, extensions included, because WhatsApp's
// short "400 Bad Request (CRITICAL)" alone does not say what it rejected.
func parseSubscribedNewsletters(result []byte) ([]*types.NewsletterMetadata, error) {
	var resp struct {
		Data struct {
			Newsletters []*types.NewsletterMetadata `json:"xwa2_newsletter_subscribed"`
		} `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal graphql response: %w; response %s", err, truncateForLog(result))
	}
	var gqlErrs types.GraphQLErrors
	if len(resp.Errors) > 0 && string(resp.Errors) != "null" {
		if err := json.Unmarshal(resp.Errors, &gqlErrs); err != nil || len(gqlErrs) == 0 {
			return nil, fmt.Errorf("unreadable graphql errors %s", truncateForLog(resp.Errors))
		}
	}
	if len(gqlErrs) > 0 && len(resp.Data.Newsletters) == 0 {
		return nil, fmt.Errorf("graphql error: %w; errors %s", gqlErrs, truncateForLog(resp.Errors))
	}
	list := resp.Data.Newsletters[:0]
	for _, m := range resp.Data.Newsletters {
		if m != nil {
			list = append(list, m)
		}
	}
	return list, nil
}

// maxLoggedResponse bounds the raw server answers copied into errors and logs.
const maxLoggedResponse = 8 << 10

func truncateForLog[T ~string | ~[]byte](s T) string {
	if len(s) <= maxLoggedResponse {
		return string(s)
	}
	return string(s[:maxLoggedResponse]) + fmt.Sprintf("… (%d bytes)", len(s))
}

// subscribedNewsletters lists the followed channels with WhatsApp Web's
// current query; see subscribedNewslettersQueryID. A failure is logged with
// the payload sent and the whole IQ answer, so a live failure can be read off
// the bridge log without another request.
func (b *Bridge) subscribedNewsletters(ctx context.Context) ([]*types.NewsletterMetadata, error) {
	payload := subscribedNewslettersPayload()
	// A raw mex query: see wa.Newsletters.SendIQ.
	resp, err := b.client.SendIQ(ctx, whatsmeow.DangerousInfoQuery{
		Namespace: "w:mex",
		Type:      whatsmeow.DangerousInfoQueryType("get"),
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag:     "query",
			Attrs:   waBinary.Attrs{"query_id": subscribedNewslettersQueryID},
			Content: payload,
		}},
	})
	list, err := readSubscribedNewsletters(resp, err)
	if err != nil {
		answer := "none"
		if resp != nil {
			answer = truncateForLog(resp.String())
		}
		b.log.Warn("channel list query failed", "query_id", subscribedNewslettersQueryID, "err", err, "sent", string(payload), "answer", answer)
	}
	return list, err
}

func readSubscribedNewsletters(resp *waBinary.Node, err error) ([]*types.NewsletterMetadata, error) {
	if err != nil {
		return nil, err
	}
	result, ok := resp.GetOptionalChildByTag("result")
	if !ok {
		return nil, fmt.Errorf("mex response has no result")
	}
	content, ok := result.Content.([]byte)
	if !ok {
		return nil, fmt.Errorf("unexpected content type %T in mex response", result.Content)
	}
	if result.AttrGetter().OptionalString("format") == "argo" {
		return nil, fmt.Errorf("mex response is argo-encoded, which the bridge cannot decode")
	}
	return parseSubscribedNewsletters(content)
}

// Channels lists the followed channels the read allowlist shows.
func (b *Bridge) Channels(ctx context.Context) ([]Channel, error) {
	list, err := b.subscribedNewsletters(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(list))
	for _, m := range list {
		if b.checkReadable(ctx, m.ID) != nil {
			continue
		}
		out = append(out, describeChannel(m))
	}
	return out, nil
}

// Channel page sizes: WhatsApp answers at most 100 posts per request.
const (
	defaultChannelMessages = 20
	maxChannelMessages     = 100
)

// ChannelMessages reads the latest posts of a channel.
func (b *Bridge) ChannelMessages(ctx context.Context, channelJID string, count, before int) ([]ChannelMessage, error) {
	jid, err := types.ParseJID(channelJID)
	if err != nil || jid.Server != types.NewsletterServer {
		return nil, fmt.Errorf("%q is not a channel JID (…@newsletter)", channelJID)
	}
	if err := b.checkReadable(ctx, jid); err != nil {
		return nil, err
	}
	if count <= 0 || count > maxChannelMessages {
		count = defaultChannelMessages
	}
	msgs, err := b.client.GetNewsletterMessages(ctx, jid, &whatsmeow.GetNewsletterMessagesParams{Count: count, Before: before})
	if err != nil {
		return nil, err
	}
	out := make([]ChannelMessage, 0, len(msgs))
	for _, m := range msgs {
		cm := ChannelMessage{
			ServerID: m.MessageServerID, ID: m.MessageID, Timestamp: m.Timestamp,
			Views: m.ViewsCount, Forwards: m.ForwardsCount, Reactions: m.ReactionCounts,
		}
		if m.Message != nil {
			cm.Text = content.Text(m.Message)
			cm.MediaType = content.Media(m.Message, m.Timestamp).Type
		}
		out = append(out, cm)
	}
	return out, nil
}

// FollowChannel follows or unfollows a channel.
func (b *Bridge) FollowChannel(ctx context.Context, req ChannelFollowRequest) SendMessageResponse {
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	meta, err := b.resolveChannel(ctx, req.Channel)
	if err != nil {
		return failed("Channel not found: %v", err)
	}
	if err := b.checkAllowed(ctx, meta.ID); err != nil {
		return blockedResponse(err)
	}
	verb := "Following"
	if req.Follow {
		err = b.client.FollowNewsletter(ctx, meta.ID)
	} else {
		verb = "Unfollowed"
		err = b.client.UnfollowNewsletter(ctx, meta.ID)
	}
	if err != nil {
		return failed("Failed: %v", err)
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("%s %s (%s)", verb, meta.ThreadMeta.Name.Text, meta.ID)}
}

// Text status colours (ARGB): white on WhatsApp's dark green.
const (
	statusBackground = 0xFF1E6E4F
	statusText       = 0xFFFFFFFF
)

// statusTextMessage is a text status: WhatsApp shows text statuses on a
// coloured background, which a plain Conversation does not have.
func statusTextMessage(text string) *waE2E.Message {
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:           new(text),
		BackgroundArgb: proto.Uint32(statusBackground),
		TextArgb:       proto.Uint32(statusText),
		Font:           waE2E.ExtendedTextMessage_SYSTEM.Enum(),
	}}
}
