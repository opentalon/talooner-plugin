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
	"log/slog"
	"sync"
	"time"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/facts"
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

	// subscription state, one entry per PR scope key. Subscription is a fact
	// (facts.md), persisted here for the plugin process's lifetime; talon-db
	// backs it in a cluster. Guarded by subMu for concurrent action calls.
	subMu sync.Mutex
	subs  map[string]subscription

	// decision audit records, keyed by (repo, pr, head_sha). Persisted before
	// each evaluate_pr response leaves; queried by explain_pr.
	decMu     sync.Mutex
	decisions map[string]Decision

	// custom tenant-CI facts (preview.*, …) asserted via assert_facts, per PR
	// scope key. Store-only: they are merged into the scope at the next
	// evaluate_pr, where they reach a verdict (decision 20). lastActivity tracks
	// the retention clock per scope; both are guarded by factMu.
	factMu        sync.Mutex
	tenantFacts   map[string]facts.Set
	lastActivity  map[string]int64
	factRetention time.Duration

	// Per-API-key request rate limiter and the caller log. Enforced once the
	// plugin is configured (the internet-facing gate; P-D3).
	limiter *rateLimiter
	logger  *slog.Logger
}

// subscription is a PR's subscription state and the unix time (seconds) the
// current state was established.
type subscription struct {
	subscribed bool
	since      int64
}

// New builds the registry with every Talooner action registered. Until
// Configure runs, the auth registry is empty — it authenticates nobody, which
// is the correct fail-closed default.
func New() *Server {
	empty, _ := auth.NewRegistry(nil)
	s := &Server{
		name:          Name,
		desc:          "OpenTalon PR reviewer: compiles a Talon ruleset against extracted PR facts and returns an abstract action list. Holds all state; never touches GitHub.",
		actions:       map[string]action{},
		auth:          empty,
		floor:         taloonerpb.ProtocolFloor,
		subs:          map[string]subscription{},
		decisions:     map[string]Decision{},
		tenantFacts:   map[string]facts.Set{},
		lastActivity:  map[string]int64{},
		factRetention: defaultFactRetention,
		limiter:       newRateLimiter(defaultRateLimit),
		logger:        slog.Default(),
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
// a caller error, returned as such rather than as a transport failure.
//
// Once the plugin is configured it is the internet-facing gate: every action
// authenticates the API key (fail closed), is rate-limited per key, and logs
// the caller — so a tenant can answer "which repo burned my quota" without a
// model in the loop. An unconfigured Server (tests/dev) skips the gate; whoami
// keeps its own fail-closed auth regardless. It satisfies plugin.Handler.
func (s *Server) Execute(req plugin.Request) plugin.Response {
	a, ok := s.actions[req.Action]
	if !ok {
		return plugin.Response{CallID: req.ID, Error: fmt.Sprintf("talooner: unknown action %q", req.Action)}
	}

	if s.auth.Configured() {
		key := req.Args[auth.ArgAPIKey]
		tenant, err := s.auth.Authenticate(key)
		if err != nil {
			return errorResponse(req, err)
		}
		if !s.limiter.Allow(key) {
			return errorResponse(req, fmt.Errorf("talooner: rate limit exceeded for this API key; slow down"))
		}
		s.logCaller(req, tenant)
	}

	return a.handler(req)
}

// logCaller records who made a call — tenant, action, repo, pr, and the
// workflow run id — so quota spend is attributable to a repo without inspecting
// any model output. The API key itself is never logged.
func (s *Server) logCaller(req plugin.Request, tenant auth.Tenant) {
	s.logger.Info("talooner action",
		"action", req.Action,
		"tenant", tenant.Name,
		"repo", req.Args["repo"],
		"pr", req.Args["pr"],
		"workflow_run_id", req.Args["workflow_run_id"],
	)
}
