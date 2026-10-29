package resources

// MCP resources for WhatsApp, and notifications when they change.
//
// Resources give clients that understand them a readable view of the
// account: recent chats, chats with unread messages, and the latest
// messages of a chat. While the server runs, an event pump per account
// reads that bridge's event stream and publishes "resource updated"
// notifications, so a client that subscribed learns about new messages
// without polling.
//
// whatsapp://chats, whatsapp://chats/unread, whatsapp://status and
// whatsapp://chat/{chat_jid} show the default account. With several
// accounts (WHATSAPP_ACCOUNTS) each one also has whatsapp://{account}/chats,
// whatsapp://{account}/chats/unread, whatsapp://{account}/status and
// whatsapp://{account}/chat/{chat_jid}; the default account has both.

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	acct "github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

const (
	chatsURI  = "whatsapp://chats"
	unreadURI = "whatsapp://chats/unread"
	statusURI = "whatsapp://status"
	chatURI   = "whatsapp://chat/{chat_jid}"
)

// Account names that would make an account's URIs read as the default
// account's (whatsapp://chats/unread is the default's unread chats, not
// the chats of an account "chats").
var reservedNames = map[string]bool{"chats": true, "chat": true, "status": true}

// DefaultRetryInterval is the pause between attempts to reach a bridge's
// event stream while it is down.
const DefaultRetryInterval = 10 * time.Second

// eventsWindow is how long one connection to a bridge's event stream is
// kept before it is reopened.
const eventsWindow = 25 * time.Second

// Deps are what the resources read with; the server owns them.
type Deps struct {
	Accounts *acct.Registry
	Pool     *store.Pool
	HTTP     *http.Client
	Scope    store.Scope
	Log      *slog.Logger
}

func (d *Deps) db(a *acct.Account) *store.DB { return store.NewDB(d.Pool, a.StoreDir, d.Scope) }

func (d *Deps) bridge(a *acct.Account) *bridgeapi.Client {
	return &bridgeapi.Client{BaseURL: a.APIURL, Token: a.Token, HTTP: d.HTTP}
}

// resourceURIs are the four URIs of an account ("": the default account's
// plain URIs).
type resourceURIs struct{ chats, unread, status, chat string }

func urisOf(account string) resourceURIs {
	prefix := "whatsapp://"
	if account != "" {
		prefix += account + "/"
	}
	return resourceURIs{prefix + "chats", prefix + "chats/unread", prefix + "status", prefix + "chat/{chat_jid}"}
}

func chatURIFor(chatJID, account string) string {
	return strings.Replace(urisOf(account).chat, "{chat_jid}", chatJID, 1)
}

// resourceJSON renders like json.dumps(indent=1), with times as
// str(datetime) ("2026-10-27 12:05:00+03:00").
func resourceJSON(v any) string {
	if chats, ok := v.([]*store.Chat); ok {
		items := []any{}
		for _, c := range chats {
			items = append(items, render.Chat(c, true))
		}
		v = items
	}
	return pyfmt.Indent(v, " ")
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

// NamedAccounts are the accounts that get URIs of their own: every
// configured account when there are several, none with just one. Names
// that cannot have URIs of their own are logged and left out.
func NamedAccounts(accounts map[string]*acct.Account, log *slog.Logger) []string {
	if len(accounts) < 2 {
		return nil
	}
	var names []string
	for _, name := range acct.SortedNames(accounts) {
		if reservedNames[name] || strings.Contains(name, "/") {
			log.Warn("account gets no resources: its URIs would clash with the default account's", "account", name)
			continue
		}
		names = append(names, name)
	}
	return names
}

// Register adds the default account's resources and those of every
// named account, and routes chat URIs to readChat.
func Register(s *mcp.Server, deps *Deps, named []string) {
	registerAccountResources(s, deps, "")
	for _, name := range named {
		registerAccountResources(s, deps, name)
	}
	// The SDK matches {chat_jid} only against unreserved characters, and a
	// JID has an "@"; chat URIs are routed here before the SDK looks.
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if r, ok := req.(*mcp.ReadResourceRequest); ok && method == "resources/read" && r.Params != nil {
				if account, jid, ok := parseChatURI(r.Params.URI, named); ok {
					return readChat(ctx, deps, r.Params.URI, account, jid)
				}
			}
			return next(ctx, method, req)
		}
	})
}

// parseChatURI splits whatsapp://[account/]chat/<jid>.
func parseChatURI(uri string, named []string) (account, jid string, ok bool) {
	rest, found := strings.CutPrefix(uri, "whatsapp://")
	if !found {
		return "", "", false
	}
	if j, found := strings.CutPrefix(rest, "chat/"); found {
		return "", unescape(j), true
	}
	name, tail, _ := strings.Cut(rest, "/")
	if j, found := strings.CutPrefix(tail, "chat/"); found {
		if slices.Contains(named, name) {
			return name, unescape(j), true
		}
	}
	return "", "", false
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// registerAccountResources registers the four resources of account ("":
// the default account's plain URIs). Each read runs against that account,
// so its store, bridge and read allowlist apply.
func registerAccountResources(s *mcp.Server, deps *Deps, account string) {
	uris := urisOf(account)
	label, suffix := "", ""
	if account != "" {
		label, suffix = " of the WhatsApp account "+pyfmt.Repr(account), "_"+account
	}
	read := func(mime string, body func(ctx context.Context, a *acct.Account) string) mcp.ResourceHandler {
		return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			a, err := deps.Accounts.Resolve(account)
			if err != nil {
				return nil, err
			}
			return textResource(req.Params.URI, mime, body(ctx, a)), nil
		}
	}
	s.AddResource(&mcp.Resource{URI: uris.chats, Name: "chats" + suffix, MIMEType: "application/json",
		Description: "The 50 most recently active WhatsApp chats with their last message" + label},
		read("application/json", func(ctx context.Context, a *acct.Account) string {
			return resourceJSON(deps.db(a).ListChats(ctx, store.ChatOpts{Limit: 50, IncludeLastMessage: true, SortBy: "last_active"}))
		}))
	s.AddResource(&mcp.Resource{URI: uris.unread, Name: "unread_chats" + suffix, MIMEType: "application/json",
		Description: "WhatsApp chats with unread messages and how many" + label},
		read("application/json", func(ctx context.Context, a *acct.Account) string {
			return resourceJSON(deps.db(a).ListUnreadChats(ctx, 100))
		}))
	s.AddResource(&mcp.Resource{URI: uris.status, Name: "bridge_status" + suffix, MIMEType: "application/json",
		Description: "Whether the WhatsApp bridge is connected, and what to do if the session is lost" + label},
		read("application/json", func(ctx context.Context, a *acct.Account) string { return resourceJSON(deps.bridge(a).Status(ctx)) }))
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: uris.chat, Name: "chat" + suffix, MIMEType: "text/plain",
		Description: "The latest 50 messages of a WhatsApp chat, newest first" + label},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			_, jid, _ := parseChatURI(req.Params.URI, []string{account})
			return readChat(ctx, deps, req.Params.URI, account, jid)
		})
}

func readChat(ctx context.Context, deps *Deps, uri, account, jid string) (*mcp.ReadResourceResult, error) {
	acc, err := deps.Accounts.Resolve(account)
	if err != nil {
		return nil, err
	}
	o := store.DefaultListOpts()
	o.ChatJID, o.Limit, o.IncludeContext = jid, 50, false
	text, err := render.ListMessages(ctx, deps.db(acc), o)
	if err != nil {
		return nil, err
	}
	return textResource(uri, "text/plain", text), nil
}

// updatedURIs are the resources of account ("": the default account's
// plain URIs) a bridge event changes.
func updatedURIs(event *pyfmt.OMap, account string) []string {
	uris := urisOf(account)
	kind, _ := event.Get("type").(string)
	if kind == "session" {
		return []string{uris.status}
	}
	chat, _ := event.Get("chat_jid").(string)
	if chat == "" || kind == "presence" {
		return nil
	}
	out := []string{chatURIFor(chat, account)}
	switch kind {
	case "message", "unavailable", "call", "group":
		out = append(out, uris.chats, uris.unread)
	}
	return out
}

// pumps says which accounts to read events of ("": the default account),
// and the URI sets each one's events update: the default account's events
// update its plain URIs and, when it is named, its named ones.
func pumps(named []string, def string) map[string][]string {
	out := map[string][]string{"": {""}}
	for _, name := range named {
		if name == def {
			out[""] = append(out[""], name)
		} else {
			out[name] = []string{name}
		}
	}
	return out
}

// Pump reads every account's bridge events and publishes the resource
// updates they mean.
type Pump struct {
	Deps *Deps
	// Named are the accounts with resources of their own.
	Named []string
	// Publish sends a "resource updated" notification.
	Publish func(uri string)
	// RetryInterval is the pause between attempts to reach a bridge's
	// event stream while it is down.
	RetryInterval time.Duration
}

// NewPump is a pump with the default retry interval.
func NewPump(deps *Deps, named []string, publish func(uri string)) *Pump {
	return &Pump{Deps: deps, Named: named, Publish: publish, RetryInterval: DefaultRetryInterval}
}

// Run publishes resource updates from every account's bridge events until
// ctx ends, and returns when all pumps have stopped.
func (p *Pump) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for account, sets := range pumps(p.Named, p.Deps.Accounts.DefaultName()) {
		wg.Go(func() { p.account(ctx, account, sets) })
	}
	wg.Wait()
}

// account publishes the updates of one account's events as each arrives
// rather than in batches. Outages are logged once, not at every retry, as
// this runs for weeks.
func (p *Pump) account(ctx context.Context, account string, sets []string) {
	log := p.Deps.Log
	if account != "" {
		log = log.With("account", account)
	}
	failing := false
	recovered := func() {
		if failing {
			log.Info("event stream back")
			failing = false
		}
	}
	for ctx.Err() == nil {
		started := time.Now()
		acc, err := p.Deps.Accounts.Resolve(account)
		if err == nil {
			err = p.Deps.bridge(acc).Events(ctx, eventsWindow, "", p.Deps.db(acc).EventFilter, func(ev *pyfmt.OMap) bool {
				recovered()
				p.publish(ev, sets)
				return true
			})
		}
		pause := time.Duration(0)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			// Bridge down or restarting: try again later.
			if !failing {
				log.Warn("event stream down", "retry_every", p.RetryInterval, "err", err)
			}
			failing = true
			pause = p.RetryInterval
		default:
			recovered()
			if time.Since(started) < time.Second {
				// A stream that ends at once must not become a busy loop.
				pause = time.Second
			}
		}
		if pause > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(pause):
			}
		}
	}
}

// publish sends the updates an event means for the URI sets, each URI once.
func (p *Pump) publish(ev *pyfmt.OMap, sets []string) {
	var uris []string
	for _, set := range sets {
		for _, uri := range updatedURIs(ev, set) {
			if !slices.Contains(uris, uri) {
				uris = append(uris, uri)
			}
		}
	}
	for _, uri := range uris {
		p.Publish(uri)
	}
}
