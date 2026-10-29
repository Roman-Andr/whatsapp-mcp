package main

// Pure-function contracts: message content extraction, the Event wire
// format, webhook signing and delivery, and every setting read from the
// environment.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

var contentTS = time.Date(2025, 4, 2, 15, 4, 5, 0, time.UTC)

func ctxInfo() *waE2E.ContextInfo {
	return &waE2E.ContextInfo{
		StanzaID: proto.String("QUOTED1"), Participant: proto.String("99990000002@lid"),
		QuotedMessage: &waE2E.Message{Conversation: proto.String("the quoted text")},
		MentionedJID:  []string{"99990000002@lid"},
	}
}

// contentCorpus is the message corpus of content.golden. business marks
// the payloads the ingest contract also stores.
var contentCorpus = []struct {
	name     string
	msg      *waE2E.Message
	business bool
}{
	{name: "nil"},
	{name: "empty", msg: &waE2E.Message{}},
	{name: "conversation", msg: &waE2E.Message{Conversation: proto.String("hello")}},
	{name: "extended text", msg: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("see https://example.com")}}},
	{name: "extended text reply", msg: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("agreed"), ContextInfo: ctxInfo()}}},
	{name: "image with caption", msg: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String("look"), Mimetype: proto.String("image/jpeg"), URL: proto.String("https://mmg.whatsapp.net/x"),
		DirectPath: proto.String("/v/x"), MediaKey: []byte{1, 2}, FileSHA256: []byte{3}, FileEncSHA256: []byte{4},
		FileLength: proto.Uint64(10), Width: proto.Uint32(800), Height: proto.Uint32(600), ContextInfo: ctxInfo(),
	}}},
	{name: "image png", msg: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/png")}}},
	{name: "image unknown mime", msg: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/x-unknown-thing")}}},
	{name: "video", msg: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("clip"), Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(30), Width: proto.Uint32(640), Height: proto.Uint32(480)}}},
	{name: "gif", msg: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true)}}},
	{name: "video note", msg: &waE2E.Message{PtvMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(9)}}},
	{name: "voice note", msg: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(7), ContextInfo: ctxInfo()}}},
	{name: "audio file", msg: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/mpeg"), Seconds: proto.Uint32(180)}}},
	{name: "document named", msg: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("report.pdf"), Mimetype: proto.String("application/pdf"), Caption: proto.String("Q1")}}},
	{name: "document unnamed", msg: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf")}}},
	{name: "document unnamed unknown", msg: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/x-nothing-known")}}},
	{name: "document with caption envelope", msg: &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("../evil/../name.txt"), Caption: proto.String("inside")}}}}},
	{name: "sticker", msg: &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), Width: proto.Uint32(512), Height: proto.Uint32(512)}}},
	{name: "animated sticker", msg: &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), IsAnimated: proto.Bool(true)}}},
	{name: "location", msg: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(55.751244), DegreesLongitude: proto.Float64(37.618423), Name: proto.String("Red Square"), Address: proto.String("Moscow")}}},
	{name: "location bare", msg: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(-1), DegreesLongitude: proto.Float64(2)}}},
	{name: "live location", msg: &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2), Caption: proto.String("coming")}}},
	{name: "contact", msg: &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Bob"), Vcard: proto.String("BEGIN:VCARD\nEND:VCARD")}}},
	{name: "contacts array", msg: &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{Contacts: []*waE2E.ContactMessage{{DisplayName: proto.String("A")}, {DisplayName: proto.String("B")}}}}},
	{name: "event", msg: &waE2E.Message{EventMessage: &waE2E.EventMessage{
		Name: proto.String("Depot meeting"), Description: proto.String(" Bring sheets "),
		StartTime: proto.Int64(time.Date(2025, 11, 1, 18, 0, 0, 0, time.UTC).Unix()), EndTime: proto.Int64(time.Date(2025, 11, 1, 20, 0, 0, 0, time.UTC).Unix()),
		Location: &waE2E.LocationMessage{Name: proto.String("North depot")}, JoinLink: proto.String("https://call.whatsapp.com/x"),
	}}},
	{name: "event cancelled multi-day", msg: &waE2E.Message{EventMessage: &waE2E.EventMessage{
		Name: proto.String("Trip"), IsCanceled: proto.Bool(true),
		StartTime: proto.Int64(time.Date(2025, 11, 1, 18, 0, 0, 0, time.UTC).Unix()), EndTime: proto.Int64(time.Date(2025, 11, 3, 9, 0, 0, 0, time.UTC).Unix()),
		Location: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(10), DegreesLongitude: proto.Float64(20)},
	}}},
	{name: "poll", msg: &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Soup")}, {OptionName: proto.String("Salad")}}}}},
	{name: "poll v2", msg: &waE2E.Message{PollCreationMessageV2: &waE2E.PollCreationMessage{Name: proto.String("V2?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("A")}}}}},
	{name: "poll v3", msg: &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("V3?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("B")}}, SelectableOptionsCount: proto.Uint32(2)}}},
	{name: "buttons response", business: true, msg: &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
		Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: "Yes, deliver"}, ContextInfo: ctxInfo(),
	}}},
	{name: "list response", business: true, msg: &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{Title: proto.String("Option 2")}}},
	{name: "template button reply", business: true, msg: &waE2E.Message{TemplateButtonReplyMessage: &waE2E.TemplateButtonReplyMessage{SelectedDisplayText: proto.String("Confirm")}}},
	{name: "buttons message", business: true, msg: &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{ContentText: proto.String("Choose a slot")}}},
	{name: "list message", business: true, msg: &waE2E.Message{ListMessage: &waE2E.ListMessage{Title: proto.String("Menu"), Description: proto.String("Pick one")}}},
	{name: "interactive", business: true, msg: &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
		Header: &waE2E.InteractiveMessage_Header{Title: proto.String("Order 42"), Subtitle: proto.String("ready")},
		Body:   &waE2E.InteractiveMessage_Body{Text: proto.String("Your code is 482913")},
		Footer: &waE2E.InteractiveMessage_Footer{Text: proto.String("Shop")},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
			Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				{Name: proto.String("cta_copy"), ButtonParamsJSON: proto.String(`{"display_text":"Copy","copy_code":"482913"}`)},
				{Name: proto.String("cta_url"), ButtonParamsJSON: proto.String(`{"display_text":"Track","url":"https://x.example/42"}`)},
				{Name: proto.String("quick_reply"), ButtonParamsJSON: proto.String(`{"display_text":"OK"}`)},
				{Name: proto.String("broken"), ButtonParamsJSON: proto.String(`not json`)},
			},
		}},
	}}},
	{name: "hydrated template", business: true, msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
		HydratedTemplate: &waE2E.TemplateMessage_HydratedFourRowTemplate{
			Title: &waE2E.TemplateMessage_HydratedFourRowTemplate_HydratedTitleText{HydratedTitleText: "DHL"}, HydratedContentText: proto.String("Parcel 123 arrives today"), HydratedFooterText: proto.String("Reply STOP"),
			HydratedButtons: []*waE2E.HydratedTemplateButton{
				{HydratedButton: &waE2E.HydratedTemplateButton_UrlButton{UrlButton: &waE2E.HydratedTemplateButton_HydratedURLButton{DisplayText: proto.String("Track"), URL: proto.String("https://dhl.example/123")}}},
				{HydratedButton: &waE2E.HydratedTemplateButton_CallButton{CallButton: &waE2E.HydratedTemplateButton_HydratedCallButton{DisplayText: proto.String("Call"), PhoneNumber: proto.String("+100")}}},
				{HydratedButton: &waE2E.HydratedTemplateButton_QuickReplyButton{QuickReplyButton: &waE2E.HydratedTemplateButton_HydratedQuickReplyButton{DisplayText: proto.String("Thanks")}}},
			},
		},
	}}},
	{name: "four-row template params", business: true, msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
		Format: &waE2E.TemplateMessage_FourRowTemplate_{FourRowTemplate: &waE2E.TemplateMessage_FourRowTemplate{
			Content: &waE2E.HighlyStructuredMessage{Params: []string{"Payment", "of", "$5"}},
			Footer:  &waE2E.HighlyStructuredMessage{Params: []string{"Bank"}},
		}},
	}}},
	{name: "interactive template", business: true, msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
		Format: &waE2E.TemplateMessage_InteractiveMessageTemplate{InteractiveMessageTemplate: &waE2E.InteractiveMessage{Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Template body")}}},
	}}},
	{name: "highly structured", business: true, msg: &waE2E.Message{HighlyStructuredMessage: &waE2E.HighlyStructuredMessage{Params: []string{"code", "1234"}}}},
	{name: "ephemeral envelope", msg: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("disappearing")}}}},
	{name: "view once image", msg: &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("once"), Mimetype: proto.String("image/jpeg")}}}}},
	{name: "view once v1", msg: &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("v1")}}}},
	{name: "view once extension", msg: &waE2E.Message{ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}}}},
	{name: "nested envelopes", msg: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("deep")}}}}}},
	{name: "device sent", msg: &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("from phone"), ContextInfo: ctxInfo()}}}}},
	{name: "reaction only", msg: &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}},
	{name: "protocol only", msg: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum()}}},
	{name: "quoted sticker", msg: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("lol"), ContextInfo: &waE2E.ContextInfo{
		StanzaID: proto.String("S"), QuotedMessage: &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}},
	}}}},
}

// contentLine renders what the content helpers make of one message.
func contentLine(msg *waE2E.Message) string {
	out := map[string]any{
		"text":  content.Text(msg),
		"media": content.Media(msg, contentTS),
	}
	if ci := content.ContextInfo(msg); ci != nil {
		quoted := map[string]any{"id": ci.GetStanzaID(), "participant": ci.GetParticipant(), "mentions": ci.GetMentionedJID()}
		if ci.GetQuotedMessage() != nil {
			quoted["preview"] = content.QuotedPreview(ci.GetQuotedMessage())
		}
		out["context"] = quoted
	}
	if poll := content.PollCreation(content.Unwrap(msg)); poll != nil {
		var options []string
		for _, o := range poll.GetOptions() {
			options = append(options, o.GetOptionName())
		}
		out["poll"] = map[string]any{"name": poll.GetName(), "options": options, "selectable": poll.GetSelectableOptionsCount()}
	}
	data, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestContractContent(t *testing.T) {
	fixLocalZone(t)
	if len(contentCorpus) < 40 {
		t.Fatalf("corpus has %d messages, want at least 40", len(contentCorpus))
	}
	var sb strings.Builder
	sb.WriteString("== messages\n")
	for _, c := range contentCorpus {
		fmt.Fprintf(&sb, "%s | %s\n", c.name, contentLine(c.msg))
	}
	sb.WriteString("== extractMediaInfo with a zero timestamp names the file after now\n")
	zero := content.Media(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, time.Time{})
	fmt.Fprintf(&sb, "image | %v\n", strings.HasPrefix(zero.Filename, "image_"+time.Now().Format("20060102")))
	sb.WriteString("== formatLocation\n")
	for _, l := range []struct {
		lat, lng      float64
		name, address string
	}{{55.75, 37.62, "", ""}, {0, 0, "Null Island", ""}, {-33.8688, 151.2093, "", "Sydney"}, {1.23456789, -9.87654321, " a ", " b "}} {
		fmt.Fprintf(&sb, "%v,%v,%q,%q | %q\n", l.lat, l.lng, l.name, l.address, content.FormatLocation(l.lat, l.lng, l.name, l.address))
	}
	sb.WriteString("== extensionFor\n")
	// Only types the code names itself: others come from the host's MIME
	// database and differ between machines.
	for _, m := range []string{"image/jpeg", "image/png", "image/webp", "video/mp4", "application/pdf", "application/pdf; name=x", "", "nonsense/type"} {
		fmt.Fprintf(&sb, "%q | %q\n", m, content.Extension(m, ".fallback"))
	}
	sb.WriteString("== mimeTypeFor\n")
	for _, p := range []string{"a.jpg", "a.JPEG", "a.png", "a.gif", "a.webp", "a.ogg", "a.mp3", "a.m4a", "a.mp4", "a.avi", "a.mov", "a.pdf", "a.txt", "a.csv", "a.json", "a.zip", "a.doc", "a.docx", "a.xls", "a.xlsx", "a.ppt", "a.pptx", "a.unknownext-contract", "noext"} {
		fmt.Fprintf(&sb, "%s | %s\n", p, content.MIMEType(p))
	}
	sb.WriteString("== safeFileName\n")
	for _, n := range []string{"report.pdf", "../../etc/passwd", `..\..\win.ini`, "a:b", "tab\there", "", ".", "..", "/", "dir/", "émoji 😀.txt"} {
		fmt.Fprintf(&sb, "%q | %q\n", n, content.SafeFileName(n))
	}
	sb.WriteString("== mediaDir\n")
	for _, c := range []string{"79990000002@s.whatsapp.net", "120363000000000001@g.us", "79990000002:5@s.whatsapp.net", "../x@lid", "status@broadcast"} {
		fmt.Fprintf(&sb, "%q | %q\n", c, mediaDir(c))
	}
	sb.WriteString("== mediaFileName\n")
	fmt.Fprintf(&sb, "%q\n", mediaFileName("3EB0ABC", "../photo.jpg"))
	checkGolden(t, "content.golden", sb.String())
}

func TestContractEventJSON(t *testing.T) {
	at := time.Date(2025, 6, 1, 12, 30, 45, 123000000, time.FixedZone("MSK", 3*3600))
	var sb strings.Builder
	line := func(name string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, "%s | %s\n", name, data)
	}
	line("event full", hub.Event{
		Type: "message", ChatJID: "c@g.us", ChatName: "Chat", MessageID: "M", SenderJID: "s@lid", SenderName: "S",
		IsFromMe: true, Content: "text", MediaType: "image", QuotedID: "Q", Emoji: "👍", State: "ringing", Timestamp: at,
	})
	line("event minimal", hub.Event{Type: "session", Timestamp: at})
	line("event zero", hub.Event{})
	line("audit entry full", audit.Entry{Time: at, Endpoint: "send", Chat: "c", MessageID: "M", TargetID: "T", Text: "x", Success: true, Result: "ok"})
	line("audit entry minimal", audit.Entry{Time: at, Endpoint: "send"})
	line("send response full", SendMessageResponse{Success: true, Message: "m", MessageID: "M", Timestamp: at})
	line("send response minimal", SendMessageResponse{})
	line("session status", SessionStatus{State: SessionConnected, Reason: "r", Since: at, NeedsAction: true})
	line("pairing info full", PairingInfo{Linked: false, QRCode: "q", QRImage: "store/qr.png", PairingCode: "ABCD", PairingErr: "e", Updated: at})
	line("pairing info minimal", PairingInfo{Linked: true})
	line("download response", DownloadMediaResponse{Success: true, Message: "m", Filename: "f", Path: "/p", MediaType: "image", Mimetype: "image/jpeg"})
	sb.WriteString("== audit preview cuts text at 200 runes\n")
	fmt.Fprintf(&sb, "%q\n", audit.Preview(strings.Repeat("я", 205), 200)[390:])
	checkGolden(t, "events.golden", sb.String())
}

func TestContractWebhook(t *testing.T) {
	var sb strings.Builder
	body := []byte(`{"type":"message","chat_jid":"c@g.us","timestamp":"2025-06-01T12:00:00Z"}`)
	fmt.Fprintf(&sb, "signature(contract-secret, 1748779200, body) = %s\n", hub.Sign("contract-secret", "1748779200", body))
	fmt.Fprintf(&sb, "signature(other-secret, 1748779200, body) = %s\n", hub.Sign("other-secret", "1748779200", body))
	fmt.Fprintf(&sb, "signature(contract-secret, 1748779201, body) = %s\n", hub.Sign("contract-secret", "1748779201", body))

	type delivery struct {
		method, path string
		header       http.Header
		body         []byte
	}
	got := make(chan delivery, 4)
	elsewhere := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/elsewhere" {
			elsewhere++
			return
		}
		got <- delivery{r.Method, r.URL.Path, r.Header.Clone(), data}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}
	}))
	defer server.Close()

	evt := hub.Event{Type: "message", ChatJID: "c@g.us", Content: "hi", Timestamp: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)}
	deliver := newWebhookDeliverer(t, server.URL+"/hook", "contract-secret")
	if err := deliver(evt); err != nil {
		t.Fatal(err)
	}
	d := <-got
	names := make([]string, 0, len(d.header))
	for name := range d.header {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(&sb, "delivery: %s %s\n", d.method, d.path)
	fmt.Fprintf(&sb, "headers: %s\n", strings.Join(names, ", "))
	fmt.Fprintf(&sb, "Content-Type: %s\n", d.header.Get("Content-Type"))
	fmt.Fprintf(&sb, "body: %s\n", d.body)
	ts := d.header.Get("X-Bridge-Timestamp")
	if sig := d.header.Get("X-Bridge-Signature"); sig != hub.Sign("contract-secret", ts, d.body) {
		t.Errorf("signature %s does not match timestamp %s", sig, ts)
	}
	fmt.Fprintf(&sb, "X-Bridge-Signature = signWebhook(secret, X-Bridge-Timestamp, body)\n")

	err := newWebhookDeliverer(t, server.URL+"/redirect", "contract-secret")(evt)
	<-got
	fmt.Fprintf(&sb, "redirect: error %q, requests to the target %d\n", fmt.Sprint(err), elsewhere)
	checkGolden(t, "webhook.golden", sb.String())
}

// configVars are every setting the bridge reads from the environment
// through its loaders (PLAN.md §1.3). WHATSAPP_BRIDGE_DIR and
// WHATSAPP_BRIDGE_ADDR are read inline by run() and are not covered here.
var configVars = []string{
	"WHATSAPP_READ_ONLY", "WHATSAPP_PRESENCE", "WHATSAPP_BRIDGE_TOKEN",
	"WHATSAPP_SEND_ALLOWLIST", "WHATSAPP_SEND_ALLOWLIST_FILE", "WHATSAPP_READ_ALLOWLIST", "WHATSAPP_READ_ALLOWLIST_FILE",
	"WHATSAPP_SEND_RATE", "WHATSAPP_SEND_BURST", "WHATSAPP_REJECT_CALLS", "WHATSAPP_REJECT_CALLS_MESSAGE",
	"WHATSAPP_WELCOME_FILE", "WHATSAPP_AUTO_DOWNLOAD", "WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB", "WHATSAPP_AUTO_DOWNLOAD_MAX_MB",
	"WHATSAPP_MEDIA_ROOTS", "XDG_DATA_HOME", "WHATSAPP_WEBHOOK_URL", "WHATSAPP_WEBHOOK_DISABLED", "WHATSAPP_WEBHOOK_SECRET",
	"WHATSAPP_HISTORY_DAYS", "WHATSAPP_DEVICE_NAME", "WHATSAPP_PAIR_PHONE",
}

func TestContractConfig(t *testing.T) {
	fixLocalZone(t)
	// Start from an empty configuration whatever the developer's shell has.
	for _, v := range append(configVars, "HOME", "TMPDIR") {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	// whatsmeow's DeviceProps is process-global; the pairing loaders change it.
	savedProps := proto.Clone(wastore.DeviceProps)
	t.Cleanup(func() { proto.Reset(wastore.DeviceProps); proto.Merge(wastore.DeviceProps, savedProps) })

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	t.Chdir(dir)
	// config.Load locates the media outbox for every setting; a case that
	// does not set HOME gets one that exists.
	t.Setenv("HOME", filepath.Join(dir, "home"))
	for _, d := range []string{"store", "home", "tmp", "xdg", "media"} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"allow.txt":      "# customers\n79990000002\n\n  120363000000000001@g.us  # the depot\n+79990000003\n   \n# end\n",
		"commas.txt":     "79990000002, 79990000003\n",
		"badallow.txt":   "79990000002\nnot a number\n",
		"welcome.json":   `{"120363000000000001@g.us": "Hi {name}!", "120363000000000002@g.us": "Welcome {mention}"}`,
		"welcome-bad.js": `["not", "an", "object"]`,
		"welcome-dm.js":  `{"79990000002@s.whatsapp.net": "hi"}`,
		"welcome-empty":  `{"120363000000000001@g.us": "  "}`,
		"token.txt":      "",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	type loader struct {
		name string
		load func() string
	}
	errText := func(err error) string { return "error: " + err.Error() }
	// Each loader of the old bridge is now config.Load plus the constructor
	// that turns the value into what the bridge uses; the golden lines keep
	// the old loader names.
	load := func() (*config.Config, error) { return config.Load(os.Getenv) }
	loaders := map[string]loader{
		"bool": {"envBool", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			return fmt.Sprintf("WHATSAPP_READ_ONLY=%v WHATSAPP_PRESENCE=%v", c.ReadOnly, c.Presence)
		}},
		"token": {"loadAPIToken", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			token, err := config.Secret(c.Token, "store/api_token", nil)
			if err != nil {
				return errText(err)
			}
			if data, _ := os.ReadFile("store/api_token"); len(data) > 0 && strings.TrimSpace(string(data)) == token && len(token) == 64 {
				os.Remove("store/api_token")
				return "generated 64 hex characters into store/api_token"
			}
			return fmt.Sprintf("%q", token)
		}},
		"send": {"loadSendAllowlist", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			a, err := policy.NewAllowlist(c.SendAllowlist)
			if err != nil {
				return errText(err)
			}
			return fmt.Sprintf("restricted=%v %q", a.Restricted(), a.Entries())
		}},
		"read": {"loadReadAllowlist", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			a, err := policy.NewAllowlist(c.ReadAllowlist)
			if err != nil {
				return errText(err)
			}
			return fmt.Sprintf("restricted=%v %q", a.Restricted(), a.Entries())
		}},
		"rate": {"loadRateLimiter", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			l := policy.NewRateLimiter(c.SendRate, c.SendBurst)
			if l == nil {
				return "unlimited"
			}
			rate, burst := l.Limits()
			return fmt.Sprintf("%.6f per second, burst %v", rate, burst)
		}},
		"calls": {"loadCallRejecter", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			r := newCallRejecter(c.RejectCalls)
			if r == nil {
				return "calls are not rejected"
			}
			return fmt.Sprintf("reject, reply %q", r.message)
		}},
		"welcome": {"loadWelcomeMessages", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			data, _ := json.Marshal(c.Welcomes)
			return string(data)
		}},
		"download": {"loadAutoDownloadConfig", func() string {
			c, err := load()
			if err != nil {
				// The old loader also returned the limits read before the
				// bad one: here, the defaults.
				d, _ := config.Load(func(k string) string {
					if strings.HasPrefix(k, "WHATSAPP_AUTO_DOWNLOAD_MAX") {
						return ""
					}
					return os.Getenv(k)
				})
				return errText(err) + fmt.Sprintf(" (config %+v)", newAutoDownloadConfig(d.AutoDownload))
			}
			return fmt.Sprintf("%+v", newAutoDownloadConfig(c.AutoDownload))
		}},
		"roots": {"loadMediaRoots", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			r, err := policy.NewMediaRoots(c.MediaRoots, c.MediaOutbox)
			if err != nil {
				return errText(err)
			}
			return fmt.Sprintf("dirs %q outbox %q", r.Dirs(), r.Outbox())
		}},
		"webhook": {"loadWebhookConfig", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			if c.Webhook.URL == "" || c.Webhook.Disabled {
				return "no webhook"
			}
			value, err := config.Secret(c.Webhook.Secret, "store/webhook_secret", nil)
			if err != nil {
				return errText(fmt.Errorf("failed to load the webhook secret: %w", err))
			}
			secret := fmt.Sprintf("%q", value)
			if data, _ := os.ReadFile("store/webhook_secret"); len(value) == 64 && strings.TrimSpace(string(data)) == value {
				os.Remove("store/webhook_secret")
				secret = "generated 64 hex characters into store/webhook_secret"
			}
			return fmt.Sprintf("url %q secret %s timeout %s", c.Webhook.URL, secret, hub.WebhookTimeout)
		}},
		"history": {"configureHistorySync", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			if err := configureHistorySync(c.HistoryDays); err != nil {
				return errText(err)
			}
			if c.HistoryDays == 0 {
				return "0 days (DeviceProps unchanged)"
			}
			return fmt.Sprintf("%d days, RequireFullSync=%v FullSyncDaysLimit=%v", c.HistoryDays, wastore.DeviceProps.GetRequireFullSync(), wastore.DeviceProps.GetHistorySyncConfig().GetFullSyncDaysLimit())
		}},
		"device": {"configureDeviceName", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			configureDeviceName(c.DeviceName)
			if c.DeviceName == "" {
				return `"" (DeviceProps unchanged)`
			}
			return fmt.Sprintf("%q, DeviceProps.Os=%q", c.DeviceName, wastore.DeviceProps.GetOs())
		}},
		"phone": {"pairPhoneNumber", func() string {
			c, err := load()
			if err != nil {
				return errText(err)
			}
			return fmt.Sprintf("%q", c.PairPhone)
		}},
	}

	cases := []struct {
		loader string
		env    map[string]string
	}{
		{"bool", nil},
		{"bool", map[string]string{"WHATSAPP_READ_ONLY": "1", "WHATSAPP_PRESENCE": "true"}},
		{"bool", map[string]string{"WHATSAPP_READ_ONLY": " YES ", "WHATSAPP_PRESENCE": "on"}},
		{"bool", map[string]string{"WHATSAPP_READ_ONLY": "0", "WHATSAPP_PRESENCE": "y"}},
		{"bool", map[string]string{"WHATSAPP_READ_ONLY": "enabled", "WHATSAPP_PRESENCE": "2"}},
		{"token", nil},
		{"token", map[string]string{"WHATSAPP_BRIDGE_TOKEN": "  from-env  "}},
		{"token", map[string]string{"WHATSAPP_BRIDGE_TOKEN": "   "}},
		{"send", nil},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST": "79990000002, +79990000003;120363000000000001@g.us\t99990000002@lid 79990000002:5@s.whatsapp.net"}},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST": "79990000002,not-a-number"}},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST_FILE": "allow.txt", "WHATSAPP_SEND_ALLOWLIST": "79990000009"}},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST_FILE": "badallow.txt"}},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST_FILE": "commas.txt"}},
		{"send", map[string]string{"WHATSAPP_SEND_ALLOWLIST_FILE": "missing.txt"}},
		{"read", nil},
		{"read", map[string]string{"WHATSAPP_READ_ALLOWLIST": "79990000002"}},
		{"read", map[string]string{"WHATSAPP_READ_ALLOWLIST_FILE": "allow.txt"}},
		{"read", map[string]string{"WHATSAPP_READ_ALLOWLIST": "@@"}},
		{"read", map[string]string{"WHATSAPP_READ_ALLOWLIST_FILE": "missing.txt"}},
		{"rate", nil},
		{"rate", map[string]string{"WHATSAPP_SEND_RATE": "20", "WHATSAPP_SEND_BURST": "5"}},
		{"rate", map[string]string{"WHATSAPP_SEND_RATE": "0"}},
		{"rate", map[string]string{"WHATSAPP_SEND_RATE": "5", "WHATSAPP_SEND_BURST": "0"}},
		{"rate", map[string]string{"WHATSAPP_SEND_RATE": "-1"}},
		{"rate", map[string]string{"WHATSAPP_SEND_RATE": "ten"}},
		{"rate", map[string]string{"WHATSAPP_SEND_BURST": "1.5"}},
		{"calls", nil},
		{"calls", map[string]string{"WHATSAPP_REJECT_CALLS": "yes"}},
		{"calls", map[string]string{"WHATSAPP_REJECT_CALLS": "1", "WHATSAPP_REJECT_CALLS_MESSAGE": "  Please write instead  "}},
		{"calls", map[string]string{"WHATSAPP_REJECT_CALLS_MESSAGE": "ignored without WHATSAPP_REJECT_CALLS"}},
		{"welcome", nil},
		{"welcome", map[string]string{"WHATSAPP_WELCOME_FILE": "welcome.json"}},
		{"welcome", map[string]string{"WHATSAPP_WELCOME_FILE": "missing.json"}},
		{"welcome", map[string]string{"WHATSAPP_WELCOME_FILE": "welcome-bad.js"}},
		{"welcome", map[string]string{"WHATSAPP_WELCOME_FILE": "welcome-dm.js"}},
		{"welcome", map[string]string{"WHATSAPP_WELCOME_FILE": "welcome-empty"}},
		{"download", nil},
		{"download", map[string]string{"WHATSAPP_AUTO_DOWNLOAD": "off"}},
		{"download", map[string]string{"WHATSAPP_AUTO_DOWNLOAD": "nonsense"}},
		{"download", map[string]string{"WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB": "1.5", "WHATSAPP_AUTO_DOWNLOAD_MAX_MB": "0"}},
		{"download", map[string]string{"WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB": "-1"}},
		{"download", map[string]string{"WHATSAPP_AUTO_DOWNLOAD_MAX_MB": "lots"}},
		{"roots", map[string]string{"HOME": "<DIR>/home", "TMPDIR": "<DIR>/tmp"}},
		{"roots", map[string]string{"XDG_DATA_HOME": "<DIR>/xdg", "TMPDIR": "<DIR>/tmp"}},
		{"roots", map[string]string{"XDG_DATA_HOME": "<DIR>/xdg", "WHATSAPP_MEDIA_ROOTS": "media: <DIR>/tmp :~/pictures", "HOME": "<DIR>/home"}},
		{"roots", map[string]string{"XDG_DATA_HOME": "<DIR>/xdg", "WHATSAPP_MEDIA_ROOTS": " : "}},
		{"roots", map[string]string{"XDG_DATA_HOME": "<DIR>/xdg", "WHATSAPP_MEDIA_ROOTS": "/"}},
		{"webhook", nil},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_URL": "https://hooks.example/wa"}},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_URL": "http://127.0.0.1:9/x", "WHATSAPP_WEBHOOK_SECRET": " s3cret "}},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_URL": "https://hooks.example/wa", "WHATSAPP_WEBHOOK_DISABLED": "true"}},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_URL": "ftp://hooks.example/wa"}},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_URL": "https://"}},
		{"webhook", map[string]string{"WHATSAPP_WEBHOOK_SECRET": "unused without a URL"}},
		{"history", nil},
		{"history", map[string]string{"WHATSAPP_HISTORY_DAYS": "30"}},
		{"history", map[string]string{"WHATSAPP_HISTORY_DAYS": "a month"}},
		{"device", nil},
		{"device", map[string]string{"WHATSAPP_DEVICE_NAME": "  Depot bridge  "}},
		{"device", map[string]string{"WHATSAPP_DEVICE_NAME": strings.Repeat("я", 51)}},
		{"phone", nil},
		{"phone", map[string]string{"WHATSAPP_PAIR_PHONE": " +79990000001 "}},
	}

	var sb strings.Builder
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			l := loaders[c.loader]
			keys := make([]string, 0, len(c.env))
			for k, v := range c.env {
				t.Setenv(k, strings.ReplaceAll(v, "<DIR>", dir))
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, len(keys))
			for j, k := range keys {
				parts[j] = fmt.Sprintf("%s=%q", k, c.env[k])
			}
			if len(parts) == 0 {
				parts = []string{"(unset)"}
			}
			fmt.Fprintf(&sb, "%s %s\n  -> %s\n", l.name, strings.Join(parts, " "), l.load())
		})
	}
	checkGolden(t, "config.golden", normalise(sb.String(), dir))
}
