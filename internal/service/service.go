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
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/internal/llm"
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

	// standalone is true when the plugin runs without an OpenTalon host (TCP
	// mode, main.go). llm_review needs the host callback channel, so in this
	// mode the feature is withdrawn from whoami and a fired llm_review degrades
	// to result="error" rather than reaching a model that isn't there.
	standalone bool

	// llmQuota is the live per-tenant llm_review call counter, keyed by tenant
	// name, seeded from config once and decremented as calls are made. Config's
	// CallsUsed is a starting point; this is the running total. Guarded by
	// llmMu, which also guards llmCache.
	llmMu    sync.Mutex
	llmQuota map[string]int64
	// llmCache is the fact-store-is-the-cache: llm_review verdicts keyed by
	// (scope, head_sha, code unit, prompt_version). A hit costs no call and no
	// spend; tln-db backs this in a cluster (llm-review.md, decision 9).
	llmCache map[string]llm.Result

	// subscription state, one entry per PR scope key. Subscription is a fact
	// (facts.md), persisted here for the plugin process's lifetime; tln-db
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
		desc:          "OpenTalon PR reviewer: compiles a Tln ruleset against extracted PR facts and returns an abstract action list. Holds all state; never touches GitHub.",
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
		llmQuota:      map[string]int64{},
		llmCache:      map[string]llm.Result{},
	}
	registerActions(s)
	return s
}

// SetStandalone marks the server as running without an OpenTalon host (TCP
// mode). It must be called before serving. In standalone mode llm_review is
// unavailable — there is no host callback channel to perform the model call.
func (s *Server) SetStandalone(v bool) { s.standalone = v }

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
	return plugin.CapabilitiesMsg{
		Name:        s.name,
		Description: s.desc,
		Actions:     acts,
		// evaluate_pr may fire llm_review, which the plugin performs by calling
		// the host builtin _subprocess.run over the callback channel. Declaring
		// this makes the host dispatch our actions over ExecuteBidi and hand us
		// a live HostCaller (protocol.md; llm-review.md). In TCP standalone mode
		// nothing reads this — the caller uses unary Execute.
		SupportsCallbacks: true,
	}
}

// Execute routes an action call to its registered handler. It is the unary
// entry point, used in TCP standalone mode where there is no host and so no
// callback channel; llm_review therefore has no model to reach here and
// degrades to result="error" (evaluate.go). It satisfies plugin.Handler.
func (s *Server) Execute(req plugin.Request) plugin.Response {
	return s.dispatch(context.Background(), req, nil)
}

// ExecuteWithCallbacks is the streaming entry point the host uses when
// SupportsCallbacks is advertised. It threads the live HostCaller to evaluate_pr
// so a fired llm_review can call the host builtin _subprocess.run. It satisfies
// plugin.StreamingHandler.
func (s *Server) ExecuteWithCallbacks(ctx context.Context, req plugin.Request, host plugin.HostCaller) plugin.Response {
	return s.dispatch(ctx, req, host)
}

// dispatch is the shared gate and router for both entry points. Once the plugin
// is configured it is the internet-facing gate: every action authenticates the
// API key (fail closed), is rate-limited per key, and logs the caller — so a
// tenant can answer "which repo burned my quota" without a model in the loop. An
// unconfigured Server (tests/dev) skips the gate; whoami keeps its own
// fail-closed auth regardless. Only evaluate_pr receives the HostCaller; every
// other action is a pure function of its request.
func (s *Server) dispatch(ctx context.Context, req plugin.Request, host plugin.HostCaller) plugin.Response {
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

	if req.Action == "evaluate_pr" {
		return s.evaluatePR(ctx, req, host)
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
