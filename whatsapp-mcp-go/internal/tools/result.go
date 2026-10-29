package tools

// How a handler's value or error becomes an MCP result: the text blocks
// and structured content clients were configured against, and the error
// texts they see.

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
)

// imageResult is an image a tool shows the model.
type imageResult struct {
	data []byte
	mime string
}

// sendResult is the result a send tool returns for an bridgeapi.Outcome.
func sendResult(o bridgeapi.Outcome) *pyfmt.OMap {
	return pyfmt.NewOMap("success", o.OK, "message", o.Message, "message_id", o.MessageID)
}

// result is the {success, message} result of a bridgeapi.Outcome.
func result(o bridgeapi.Outcome) *pyfmt.OMap {
	return pyfmt.NewOMap("success", o.OK, "message", o.Message)
}

func textContent(s string) mcp.Content { return &mcp.TextContent{Text: s} }

// errWrongType means a handler returned a value its output kind does not
// take: a programming error, reported like any other failure.
var errWrongType = errors.New("handler returned the wrong type")

// as is v as a T, or errWrongType.
func as[T any](v any) (T, error) {
	t, ok := v.(T)
	if !ok {
		return t, fmt.Errorf("%w: %T", errWrongType, v)
	}
	return t, nil
}

// itemsResult renders a list: one text block per item (pydantic JSON) and
// the list as the structured result.
func itemsResult[T any](v any, item func(T) *pyfmt.OMap) (*mcp.CallToolResult, error) {
	list, err := as[[]T](v)
	if err != nil {
		return nil, err
	}
	res := &mcp.CallToolResult{Content: []mcp.Content{}}
	items := []any{}
	for _, x := range list {
		m := item(x)
		items = append(items, m)
		res.Content = append(res.Content, textContent(pyfmt.Indent(m, "  ")))
	}
	res.StructuredContent = pyfmt.NewOMap("result", items)
	return res, nil
}

func chatItem(c *store.Chat) *pyfmt.OMap { return render.Chat(c, false) }

// toolResult renders a handler's value as MCP content: one text block per
// list item (pydantic JSON), and the structured result, wrapped in
// {"result": ...} unless the tool returns a typed dict.
func toolResult(kind outputKind, v any) (*mcp.CallToolResult, error) {
	switch kind {
	case outChats:
		return itemsResult(v, chatItem)
	case outContacts:
		return itemsResult(v, render.Contact)
	case outDictList:
		return itemsResult(v, func(m *pyfmt.OMap) *pyfmt.OMap { return m })
	case outOptChat:
		c, err := as[*store.Chat](v)
		if err != nil {
			return nil, err
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{}}
		var item any
		if c != nil {
			m := chatItem(c)
			item = m
			res.Content = append(res.Content, textContent(pyfmt.Indent(m, "  ")))
		}
		res.StructuredContent = pyfmt.NewOMap("result", item)
		return res, nil
	case outImage:
		return imageToolResult(v)
	}
	return scalarResult(kind, v)
}

// scalarResult renders the output kinds of one value.
func scalarResult(kind outputKind, v any) (*mcp.CallToolResult, error) {
	res := &mcp.CallToolResult{Content: []mcp.Content{}}
	switch kind {
	case outText:
		s, err := as[string](v)
		if err != nil {
			return nil, err
		}
		res.Content = append(res.Content, textContent(s))
		res.StructuredContent = pyfmt.NewOMap("result", s)
	case outOptText:
		p, err := as[*string](v)
		if err != nil {
			return nil, err
		}
		if p != nil {
			res.Content = append(res.Content, textContent(*p))
		}
		res.StructuredContent = pyfmt.NewOMap("result", pyfmt.Ptr(p))
	case outResult, outSendResult, outDict:
		m, err := as[*pyfmt.OMap](v)
		if err != nil {
			return nil, err
		}
		res.Content = append(res.Content, textContent(pyfmt.Indent(m, "  ")))
		if kind == outDict {
			res.StructuredContent = pyfmt.NewOMap("result", m)
		} else {
			res.StructuredContent = m
		}
	}
	return res, nil
}

// imageToolResult is an image, or the dict that explains why there is
// none, without structured content.
func imageToolResult(v any) (*mcp.CallToolResult, error) {
	res := &mcp.CallToolResult{Content: []mcp.Content{}}
	switch x := v.(type) {
	case imageResult:
		res.Content = append(res.Content, &mcp.ImageContent{Data: x.data, MIMEType: x.mime})
	case *pyfmt.OMap:
		res.Content = append(res.Content, textContent(pyfmt.Indent(x, "  ")))
	default:
		return nil, fmt.Errorf("%w: %T", errWrongType, v)
	}
	return res, nil
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{textContent(text)}, IsError: true}
}

// failed is the result of a call that failed with err, which is logged.
// Only a rejected argument is shown in full; any other error stays in the
// log, as it can contain paths and SQL.
func failed(log *slog.Logger, tool string, err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, ErrValidation):
		log.Info("tool rejected arguments", "tool", tool, "err", err)
		return errorResult(fmt.Sprintf("Error executing tool %s: %v", tool, err))
	case errors.Is(err, store.ErrInvalidArgument):
		log.Info("tool rejected arguments", "tool", tool, "err", err)
	default:
		log.Error("tool failed", "tool", tool, "err", err)
	}
	return errorResult("Error executing tool " + tool)
}

// panicked is the result of a call that panicked with r, which is logged.
func panicked(log *slog.Logger, tool string, r any) *mcp.CallToolResult {
	log.Error("tool panicked", "tool", tool, "panic", r)
	return errorResult("Error executing tool " + tool)
}
