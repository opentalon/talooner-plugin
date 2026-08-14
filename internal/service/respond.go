package service

import (
	"fmt"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// errorResponse returns a failed response carrying err's message. Handler
// errors are constructed so they are safe to surface: they never name a tenant
// a caller has not authenticated as.
func errorResponse(req plugin.Request, err error) plugin.Response {
	return plugin.Response{CallID: req.ID, Error: err.Error()}
}

// structuredResponse marshals a contract message into structured_content — the
// real return channel — and attaches a human-readable summary for logs and
// direct invocation (protocol.md).
func structuredResponse(req plugin.Request, msg proto.Message, summary string) plugin.Response {
	data, err := protojson.Marshal(msg)
	if err != nil {
		return plugin.Response{CallID: req.ID, Error: fmt.Sprintf("talooner: encode response: %v", err)}
	}
	return plugin.Response{CallID: req.ID, Content: summary, StructuredContent: string(data)}
}
