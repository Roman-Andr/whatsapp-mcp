package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

var _ Service = (*bridge.Bridge)(nil)

// fakeCall is one call the handlers made to the service.
type fakeCall struct {
	method string
	req    any
	// timeout is the deadline the context carried, rounded to seconds; 0
	// without one.
	timeout time.Duration
}

// fakeService is a Service that records every call and answers from its
// fields. A test that needs other behaviour for one method embeds it and
// overrides that method.
type fakeService struct {
	mu    sync.Mutex
	calls []fakeCall

	resp       bridge.SendMessageResponse // every call answering a SendMessageResponse
	group      bridge.GroupChangeResponse // every group change
	groupInfo  bridge.GroupInfoResponse   // GroupInfo
	contact    bridge.ContactInfo         // ContactInfo
	body       map[string]any             // GroupRequests, SubGroups, Blocklist
	bodyStatus int                        // their status
	download   *bridge.DownloadMediaResponse
	presence   bridge.PresenceInfo
	channels   []bridge.Channel
	channelMsg []bridge.ChannelMessage
	status     map[string]any
	pairing    bridge.PairingInfo
	qr         []byte
	online     bool
	err        error           // DownloadMedia, Presence, Channels, ChannelMessages, QRImage, Reconnect
	unreadable map[string]bool // chats CheckReadable refuses
	restarted  bool
}

func (f *fakeService) record(ctx context.Context, method string, req any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := fakeCall{method: method, req: req}
	if deadline, ok := ctx.Deadline(); ok {
		c.timeout = time.Until(deadline).Round(time.Second)
	}
	f.calls = append(f.calls, c)
}

// methods lists the recorded calls by method name.
func (f *fakeService) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, c := range f.calls {
		names = append(names, c.method)
	}
	return names
}

// last is the last recorded call.
func (f *fakeService) last() fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return fakeCall{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeService) send(ctx context.Context, method string, req any) bridge.SendMessageResponse {
	f.record(ctx, method, req)
	return f.resp
}

func (f *fakeService) change(ctx context.Context, method string, req any) bridge.GroupChangeResponse {
	f.record(ctx, method, req)
	return f.group
}

func (f *fakeService) Send(ctx context.Context, req bridge.SendMessageRequest) bridge.SendMessageResponse {
	return f.send(ctx, "Send", req)
}

func (f *fakeService) React(ctx context.Context, req bridge.ReactRequest) bridge.SendMessageResponse {
	return f.send(ctx, "React", req)
}

func (f *fakeService) SendPoll(ctx context.Context, req bridge.PollRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SendPoll", req)
}

func (f *fakeService) SendLocation(ctx context.Context, req bridge.LocationRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SendLocation", req)
}

func (f *fakeService) Edit(ctx context.Context, req bridge.EditRequest) bridge.SendMessageResponse {
	return f.send(ctx, "Edit", req)
}

func (f *fakeService) Delete(ctx context.Context, req bridge.DeleteRequest) bridge.SendMessageResponse {
	return f.send(ctx, "Delete", req)
}

func (f *fakeService) MarkRead(ctx context.Context, req bridge.ReadRequest) bridge.SendMessageResponse {
	return f.send(ctx, "MarkRead", req)
}

func (f *fakeService) SendTyping(ctx context.Context, req bridge.TypingRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SendTyping", req)
}

func (f *fakeService) VotePoll(ctx context.Context, req bridge.VoteRequest) bridge.SendMessageResponse {
	return f.send(ctx, "VotePoll", req)
}

func (f *fakeService) SendContact(ctx context.Context, req bridge.ContactRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SendContact", req)
}

func (f *fakeService) Forward(ctx context.Context, req bridge.ForwardRequest) bridge.SendMessageResponse {
	return f.send(ctx, "Forward", req)
}

func (f *fakeService) Star(ctx context.Context, req bridge.StarRequest) bridge.SendMessageResponse {
	return f.send(ctx, "Star", req)
}

func (f *fakeService) RequestHistory(ctx context.Context, req bridge.HistoryRequest) bridge.SendMessageResponse {
	return f.send(ctx, "RequestHistory", req)
}

func (f *fakeService) DownloadMedia(ctx context.Context, messageID, chatJID string) (*bridge.DownloadMediaResponse, error) {
	f.record(ctx, "DownloadMedia", messageID+"/"+chatJID)
	return f.download, f.err
}

func (f *fakeService) CheckReadable(_ context.Context, chatJID string) error {
	if f.unreadable[chatJID] {
		return &readError{chatJID}
	}
	return nil
}

// readError stands in for the bridge's read-allowlist refusal.
type readError struct{ chat string }

func (e *readError) Error() string { return "chat " + e.chat + " is not in the read allowlist" }

func (f *fakeService) GroupInfo(ctx context.Context, req bridge.GroupInfoRequest) bridge.GroupInfoResponse {
	f.record(ctx, "GroupInfo", req)
	return f.groupInfo
}

func (f *fakeService) CreateGroup(ctx context.Context, req bridge.CreateGroupRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "CreateGroup", req)
}

func (f *fakeService) LeaveGroup(ctx context.Context, req bridge.GroupChatRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "LeaveGroup", req)
}

func (f *fakeService) UpdateParticipants(ctx context.Context, req bridge.GroupParticipantsRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "UpdateParticipants", req)
}

func (f *fakeService) UpdateGroupSettings(ctx context.Context, req bridge.GroupSettingsRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "UpdateGroupSettings", req)
}

func (f *fakeService) JoinGroup(ctx context.Context, req bridge.JoinGroupRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "JoinGroup", req)
}

func (f *fakeService) GroupRequests(ctx context.Context, chatJID string) (map[string]any, int) {
	f.record(ctx, "GroupRequests", chatJID)
	return f.body, f.bodyStatus
}

func (f *fakeService) UpdateGroupRequests(ctx context.Context, req bridge.GroupRequestsUpdate) bridge.GroupChangeResponse {
	return f.change(ctx, "UpdateGroupRequests", req)
}

func (f *fakeService) ResetInviteLink(ctx context.Context, chatJID string) bridge.GroupChangeResponse {
	return f.change(ctx, "ResetInviteLink", chatJID)
}

func (f *fakeService) SetDisappearing(ctx context.Context, req bridge.DisappearingRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SetDisappearing", req)
}

func (f *fakeService) LinkCommunity(ctx context.Context, req bridge.CommunityLinkRequest) bridge.GroupChangeResponse {
	return f.change(ctx, "LinkCommunity", req)
}

func (f *fakeService) SubGroups(ctx context.Context, communityJID string) (map[string]any, int) {
	f.record(ctx, "SubGroups", communityJID)
	return f.body, f.bodyStatus
}

func (f *fakeService) ChatAction(ctx context.Context, req bridge.ChatActionRequest) bridge.SendMessageResponse {
	return f.send(ctx, "ChatAction", req)
}

func (f *fakeService) LabelChat(ctx context.Context, req bridge.LabelChatRequest) bridge.SendMessageResponse {
	return f.send(ctx, "LabelChat", req)
}

func (f *fakeService) EditLabel(ctx context.Context, req bridge.LabelEditRequest) bridge.SendMessageResponse {
	return f.send(ctx, "EditLabel", req)
}

func (f *fakeService) ContactInfo(ctx context.Context, req bridge.ContactInfoRequest) bridge.ContactInfo {
	f.record(ctx, "ContactInfo", req)
	return f.contact
}

func (f *fakeService) Blocklist(ctx context.Context) (map[string]any, int) {
	f.record(ctx, "Blocklist", nil)
	return f.body, f.bodyStatus
}

func (f *fakeService) SetBlocked(ctx context.Context, req bridge.BlockRequest) bridge.SendMessageResponse {
	return f.send(ctx, "SetBlocked", req)
}

func (f *fakeService) Presence(ctx context.Context, jid string) (bridge.PresenceInfo, error) {
	f.record(ctx, "Presence", jid)
	return f.presence, f.err
}

func (f *fakeService) Online() bool { return f.online }

func (f *fakeService) Channels(ctx context.Context) ([]bridge.Channel, error) {
	f.record(ctx, "Channels", nil)
	return f.channels, f.err
}

func (f *fakeService) ChannelMessages(ctx context.Context, channelJID string, count, before int) ([]bridge.ChannelMessage, error) {
	f.record(ctx, "ChannelMessages", []any{channelJID, count, before})
	return f.channelMsg, f.err
}

func (f *fakeService) FollowChannel(ctx context.Context, req bridge.ChannelFollowRequest) bridge.SendMessageResponse {
	return f.send(ctx, "FollowChannel", req)
}

func (f *fakeService) Status() map[string]any { return f.status }

func (f *fakeService) Pairing() bridge.PairingInfo { return f.pairing }

func (f *fakeService) QRImage() ([]byte, error) { return f.qr, f.err }

func (f *fakeService) Logout(ctx context.Context) bridge.SendMessageResponse {
	return f.send(ctx, "Logout", nil)
}

func (f *fakeService) RequestRestart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = true
}

func (f *fakeService) Reconnect() error {
	f.record(context.Background(), "Reconnect", nil)
	return f.err
}

// fakeAPI is the API built on a fake service, with an audit log in a
// temporary directory.
type fakeAPI struct {
	fake      *fakeService
	handler   http.Handler
	auditPath string
}

// newFakeAPI builds the API around fake; deps may change the Deps first.
func newFakeAPI(t *testing.T, fake *fakeService, deps func(*Deps)) *fakeAPI {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	auditLog, err := audit.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditLog.Close() })
	d := Deps{Bridge: fake, Token: "tok", Audit: auditLog, Events: events.NewHub(nil)}
	if deps != nil {
		deps(&d)
	}
	return &fakeAPI{fake: fake, handler: NewHandler(d), auditPath: path}
}

// do sends one request with the token.
func (a *fakeAPI) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-API-Token", "tok")
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

// audited is the audit log so far, without the time stamps.
func (a *fakeAPI) audited(t *testing.T) []audit.Entry {
	t.Helper()
	f, err := os.Open(a.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var entries []audit.Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e audit.Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", scanner.Text(), err)
		}
		e.Time = time.Time{}
		entries = append(entries, e)
	}
	return entries
}

// routeCase is one request against the fake API and everything it should
// cause: the exact answer, the service calls and the audit entries.
type routeCase struct {
	name   string
	method string // POST when empty
	path   string
	body   string
	deps   func(*Deps)
	fake   func(*fakeService)

	code    int
	want    string            // exact response body
	header  map[string]string // response headers that must match
	calls   []string          // service methods called, in order
	timeout time.Duration     // context deadline of the last call
	audit   []audit.Entry     // audit entries, without time stamps
	check   func(*testing.T, *fakeService)
}

func runRouteCases(t *testing.T, cases []routeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeService{}
			if c.fake != nil {
				c.fake(fake)
			}
			api := newFakeAPI(t, fake, c.deps)
			method := c.method
			if method == "" {
				method = http.MethodPost
			}
			rec := api.do(method, c.path, c.body)
			if rec.Code != c.code || rec.Body.String() != c.want {
				t.Errorf("answer %d %q, want %d %q", rec.Code, rec.Body.String(), c.code, c.want)
			}
			for k, v := range c.header {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}
			if got := fake.methods(); !slices.Equal(got, c.calls) {
				t.Errorf("calls %v, want %v", got, c.calls)
			}
			if got := fake.last().timeout; got != c.timeout {
				t.Errorf("timeout %v, want %v", got, c.timeout)
			}
			if got := api.audited(t); !slices.Equal(got, c.audit) {
				t.Errorf("audit %+v, want %+v", got, c.audit)
			}
			if c.check != nil {
				c.check(t, fake)
			}
		})
	}
}

// Answers the fake gives and the bodies they become.
var (
	sentOK     = bridge.SendMessageResponse{Success: true, Message: "sent", MessageID: "M1"}
	sentOKBody = `{"success":true,"message":"sent","message_id":"M1"}` + "\n"
	sendFailed = bridge.SendMessageResponse{Message: "Not connected to WhatsApp"}
	failedBody = `{"success":false,"message":"Not connected to WhatsApp"}` + "\n"
	refused    = bridge.SendMessageResponse{Message: "chat x is not in the send allowlist", Refused: true}
	refuseBody = `{"success":false,"message":"chat x is not in the send allowlist"}` + "\n"
)

func answer(resp bridge.SendMessageResponse) func(*fakeService) {
	return func(f *fakeService) { f.resp = resp }
}

func unreadable(chats ...string) func(*fakeService) {
	return func(f *fakeService) {
		f.unreadable = map[string]bool{}
		for _, c := range chats {
			f.unreadable[c] = true
		}
	}
}

func readOnly(d *Deps) { d.ReadOnly = true }

// exhausted is a rate limit of one call a minute whose call is used up, on
// a frozen clock.
func exhausted(d *Deps) {
	l := policy.NewRateLimiter(1, 1)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	l.Now = func() time.Time { return now }
	l.Take()
	d.Limiter = l
}

const (
	readOnlyBody = `{"success":false,"message":"` + msgReadOnly + `"}` + "\n"
	limitMsg     = "send rate limit reached (WHATSAPP_SEND_RATE); try again in 1m1s"
	limitBody    = `{"success":false,"message":"` + limitMsg + `"}` + "\n"
)
