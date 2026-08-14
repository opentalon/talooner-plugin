// Package service implements the OpenTalon PluginService for Talooner.
//
// The plugin defines no rpcs of its own. OpenTalon fixes the service in
// opentalon/proto/plugin.proto; a plugin declares *actions* and the host
// routes every call to Execute. This package is the action registry and the
// dispatcher: New builds the registry, Capabilities advertises it, and Execute
// routes an incoming action name to its handler. Action bodies are filled in
// by later phases (whoami P-B1, ruleset loader P-B2/P-B4, evaluate_pr P-B7,
// subscription P-B8, assert_facts P-C4, explain_pr P-C5).
package service

import (
	"fmt"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// Name is the plugin name the host registers this process under. Calls arrive
// as (plugin=Name, action=<action name>).
const Name = "talooner"

// HandlerFunc executes one action. Structured results travel as JSON in
// Response.StructuredContent (the real return channel — args and results are
// strings on the wire); Response.Content carries a human-readable summary for
// logs and direct invocation. See protocol.md.
type HandlerFunc func(req plugin.Request) plugin.Response

type action struct {
	def     plugin.ActionMsg
	handler HandlerFunc
}

// Server holds the action registry and implements plugin.Handler.
type Server struct {
	name    string
	desc    string
	order   []string // registration order, so Capabilities output is stable
	actions map[string]action

	auth  *auth.Registry // key→tenant; nil-safe via the empty registry set in New
	floor uint32         // lowest caller protocol_version served
}

// New builds the registry with every Talooner action registered. Until
// Configure runs, the auth registry is empty — it authenticates nobody, which
// is the correct fail-closed default.
func New() *Server {
	empty, _ := auth.NewRegistry(nil)
	s := &Server{
		name:    Name,
		desc:    "OpenTalon PR reviewer: compiles a Talon ruleset against extracted PR facts and returns an abstract action list. Holds all state; never touches GitHub.",
		actions: map[string]action{},
		auth:    empty,
		floor:   taloonerpb.ProtocolFloor,
	}
	registerActions(s)
	return s
}

// register adds one action to the registry.
//
// Every Talooner action must be user_only: an LLM anywhere in the cluster must
// never reach the decision path, or it could invoke evaluate_pr with invented
// arguments and have the bot execute the result against a real repo
// (protocol.md, "Every action must set user_only: true"). register panics on a
// non-user_only action so the mistake fails at startup rather than in
// production; a test additionally asserts the invariant over the built
// capability set, which is the regression to catch when an action is added
// later.
func (s *Server) register(def plugin.ActionMsg, h HandlerFunc) {
	if !def.UserOnly {
		panic(fmt.Sprintf("service: action %q must set user_only", def.Name))
	}
	if _, dup := s.actions[def.Name]; dup {
		panic(fmt.Sprintf("service: action %q registered twice", def.Name))
	}
	s.order = append(s.order, def.Name)
	s.actions[def.Name] = action{def: def, handler: h}
}

// Capabilities advertises the plugin's actions to the host, in registration
// order. It satisfies plugin.Handler.
func (s *Server) Capabilities() plugin.CapabilitiesMsg {
	acts := make([]plugin.ActionMsg, 0, len(s.order))
	for _, name := range s.order {
		acts = append(acts, s.actions[name].def)
	}
	return plugin.CapabilitiesMsg{Name: s.name, Description: s.desc, Actions: acts}
}

// Execute routes an action call to its registered handler. Unknown actions are
// a caller error, returned as such rather than as a transport failure. It
// satisfies plugin.Handler.
func (s *Server) Execute(req plugin.Request) plugin.Response {
	a, ok := s.actions[req.Action]
	if !ok {
		return plugin.Response{CallID: req.ID, Error: fmt.Sprintf("talooner: unknown action %q", req.Action)}
	}
	return a.handler(req)
}
