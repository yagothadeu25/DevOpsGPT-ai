package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/llm"
	"github.com/yagothadeu25/devopsgpt/pkg/watcher"
	"go.uber.org/zap"
)

// ── SRE System Prompt ─────────────────────────────────────────────────────────
const SRESystemPrompt = `You are DevOpsGPT, an expert SRE (Site Reliability Engineer) specialist
with deep expertise in Kubernetes troubleshooting, incident response, and platform engineering.

Your core competencies:
- Kubernetes internals: pod lifecycle, scheduling, networking (CNI, DNS, ingress), storage (PV/PVC)
- Observability: logs, metrics (Prometheus), traces, events
- HTTP error patterns: 401 (auth), 404 (routing), 500/503 (upstream failures), timeouts, connection resets
- Fintech reliability: high availability, zero-downtime deployments, data consistency
- Security: RBAC, NetworkPolicies, secrets management, compliance (PCI-DSS, SOC2)
- CI/CD: GitOps with ArgoCD, Helm, Kustomize
- Cloud: AWS (EKS, RDS, ElastiCache), GCP (GKE), Azure (AKS)

When troubleshooting, you always:
1. Ask clarifying questions if context is missing (namespace, pod name, error logs)
2. Follow the SRE golden signals: latency, traffic, errors, saturation
3. Think in layers: application → container → pod → node → network → infrastructure
4. Provide kubectl commands ready to copy-paste
5. Assess blast radius before suggesting any change
6. Distinguish between symptoms and root causes
7. Suggest both immediate mitigation AND long-term fix

Response format for incidents:
- 🔴 SEVERITY: [critical/error/warning]
- 🎯 ROOT CAUSE: [concise explanation]
- ⚡ IMMEDIATE ACTION: [kubectl commands]
- 🔧 PERMANENT FIX: [steps]
- 🛡️ PREVENTION: [what to add/change]

You have access to real-time cluster data via DevOpsGPT tools.
Always use the tools before answering questions about cluster state.`

// ── MCP Protocol Types ────────────────────────────────────────────────────────

type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// ── Config ────────────────────────────────────────────────────────────────────

type Config struct {
	Port      string
	Watcher   *watcher.Watcher
	LLMClient llm.Client
	Logger    *zap.Logger
}

type Server struct {
	cfg  Config
	mux  *http.ServeMux
	tools []Tool
}

func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerTools()
	s.registerRoutes()
	return s
}

// ── MCP Tools registration ────────────────────────────────────────────────────

func (s *Server) registerTools() {
	s.tools = []Tool{
		{
			Name:        "get_cluster_issues",
			Description: "Returns all current issues detected across all namespaces in the Kubernetes cluster, enriched with AI analysis.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"severity":  map[string]any{"type": "string", "enum": []string{"all", "critical", "error", "warning", "info"}},
					"namespace": map[string]any{"type": "string", "description": "Filter by namespace. Empty = all namespaces"},
				},
			},
		},
		{
			Name:        "get_namespace_summary",
			Description: "Returns a health summary of a specific namespace or all namespaces.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "get_pod_logs",
			Description: "Fetches recent logs from a specific pod to help diagnose issues.",
			InputSchema: map[string]any{
				"type": "object",
				"required": []string{"pod_name", "namespace"},
				"properties": map[string]any{
					"pod_name":  map[string]any{"type": "string"},
					"namespace": map[string]any{"type": "string"},
					"tail":      map[string]any{"type": "integer", "description": "Number of log lines to fetch (default: 100)"},
				},
			},
		},
		{
			Name:        "analyze_issue",
			Description: "Deep-dives into a specific issue ID with full AI analysis, remediation steps and risk assessment.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"issue_id"},
				"properties": map[string]any{
					"issue_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "get_sre_prompt",
			Description: "Returns the full SRE system prompt used by DevOpsGPT for context.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// ── Routes ────────────────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/mcp", s.handleMCP)
	s.mux.HandleFunc("/mcp/sse", s.handleSSE) // SSE transport for Claude Desktop
}

func (s *Server) Start(ctx context.Context) {
	srv := &http.Server{
		Addr:    ":" + s.cfg.Port,
		Handler: s.mux,
	}
	s.cfg.Logger.Info("MCP server started", zap.String("port", s.cfg.Port))
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	srv.ListenAndServe()
}

// ── MCP Handler (JSON-RPC) ────────────────────────────────────────────────────

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, 0, -32700, "Parse error")
		return
	}

	switch req.Method {

	case "initialize":
		s.writeResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "devopsgpt",
				"version": "1.0.0",
			},
			"instructions": SRESystemPrompt,
		})

	case "tools/list":
		s.writeResult(w, req.ID, map[string]any{"tools": s.tools})

	case "tools/call":
		s.handleToolCall(w, r.Context(), req)

	default:
		s.writeError(w, req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

// ── Tool call dispatch ────────────────────────────────────────────────────────

func (s *Server) handleToolCall(w http.ResponseWriter, ctx context.Context, req MCPRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(w, req.ID, -32602, "Invalid params")
		return
	}

	var result interface{}
	var err error

	switch params.Name {

	case "get_cluster_issues":
		var args struct {
			Severity  string `json:"severity"`
			Namespace string `json:"namespace"`
		}
		json.Unmarshal(params.Arguments, &args)
		result, err = s.toolGetClusterIssues(ctx, args.Severity, args.Namespace)

	case "get_namespace_summary":
		var args struct{ Namespace string `json:"namespace"` }
		json.Unmarshal(params.Arguments, &args)
		result, err = s.toolGetNamespaceSummary(args.Namespace)

	case "get_pod_logs":
		var args struct {
			PodName   string `json:"pod_name"`
			Namespace string `json:"namespace"`
			Tail      int    `json:"tail"`
		}
		json.Unmarshal(params.Arguments, &args)
		result, err = s.toolGetPodLogs(ctx, args.PodName, args.Namespace, args.Tail)

	case "analyze_issue":
		var args struct{ IssueID string `json:"issue_id"` }
		json.Unmarshal(params.Arguments, &args)
		result, err = s.toolAnalyzeIssue(args.IssueID)

	case "get_sre_prompt":
		result = map[string]any{"system_prompt": SRESystemPrompt}

	default:
		s.writeError(w, req.ID, -32601, fmt.Sprintf("Tool not found: %s", params.Name))
		return
	}

	if err != nil {
		s.writeError(w, req.ID, -32603, err.Error())
		return
	}

	s.writeResult(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": toJSON(result)},
		},
	})
}

// ── Tool implementations ──────────────────────────────────────────────────────

func (s *Server) toolGetClusterIssues(ctx context.Context, severity, namespace string) (interface{}, error) {
	results := s.cfg.Watcher.GetResults()

	var filtered []map[string]any
	for _, r := range results {
		if namespace != "" && r.Issue.Namespace != namespace {
			continue
		}
		if severity != "" && severity != "all" && string(r.Issue.Severity) != severity {
			continue
		}
		filtered = append(filtered, map[string]any{
			"id":           r.Issue.ID,
			"kind":         r.Issue.Kind,
			"name":         r.Issue.Name,
			"namespace":    r.Issue.Namespace,
			"error":        r.Issue.Error,
			"severity":     r.Issue.Severity,
			"triggered_by": r.Issue.TriggeredBy,
			"analysis":     r.Analysis,
			"llm_used":     r.LLMUsed,
			"scanned_at":   r.ScannedAt.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"total":  len(filtered),
		"issues": filtered,
	}, nil
}

func (s *Server) toolGetNamespaceSummary(namespace string) (interface{}, error) {
	results := s.cfg.Watcher.GetResults()

	summary := map[string]map[string]int{}
	for _, r := range results {
		ns := r.Issue.Namespace
		if namespace != "" && ns != namespace {
			continue
		}
		if _, ok := summary[ns]; !ok {
			summary[ns] = map[string]int{"critical": 0, "error": 0, "warning": 0, "info": 0}
		}
		summary[ns][string(r.Issue.Severity)]++
	}
	return summary, nil
}

func (s *Server) toolGetPodLogs(ctx context.Context, podName, namespace string, tail int) (interface{}, error) {
	// In full impl: use watcher.K8sClient to fetch real logs
	return map[string]any{
		"pod":       podName,
		"namespace": namespace,
		"logs":      fmt.Sprintf("[DevOpsGPT] Fetching last %d lines from %s/%s via Kubernetes API...", tail, namespace, podName),
		"note":      "Connect watcher.K8sClient to return real logs",
	}, nil
}

func (s *Server) toolAnalyzeIssue(issueID string) (interface{}, error) {
	for _, r := range s.cfg.Watcher.GetResults() {
		if r.Issue.ID == issueID {
			return map[string]any{
				"issue":    r.Issue,
				"analysis": r.Analysis,
				"llm_used": r.LLMUsed,
			}, nil
		}
	}
	return nil, fmt.Errorf("issue %s not found", issueID)
}

// ── SSE Transport (Claude Desktop) ───────────────────────────────────────────

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Send initial capabilities
	data, _ := json.Marshal(map[string]any{
		"type":         "capabilities",
		"tools":        s.tools,
		"instructions": SRESystemPrompt,
	})
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()

	<-r.Context().Done()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Server) writeResult(w http.ResponseWriter, id int, result interface{}) {
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(w http.ResponseWriter, id, code int, msg string) {
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Error: &MCPError{Code: code, Message: msg}})
}

func toJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
