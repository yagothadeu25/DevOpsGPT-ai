package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/llm"
	"github.com/yagothadeu25/devopsgpt/pkg/prompt"
	"github.com/yagothadeu25/devopsgpt/pkg/watcher"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

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

type Config struct {
	Port      string
	Watcher   *watcher.Watcher
	LLMClient llm.Client
	Logger    *zap.Logger
}

type Server struct {
	cfg   Config
	mux   *http.ServeMux
	tools []Tool
}

func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerTools()
	s.registerRoutes()
	return s
}

func (s *Server) registerTools() {
	s.tools = []Tool{
		{
			Name:        "get_cluster_issues",
			Description: "Returns all current issues detected across all namespaces, enriched with AI analysis.",
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
				"type":       "object",
				"properties": map[string]any{"namespace": map[string]any{"type": "string"}},
			},
		},
		{
			Name:        "get_pod_logs",
			Description: "Fetches recent logs from a specific pod to help diagnose issues.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"pod_name", "namespace"},
				"properties": map[string]any{
					"pod_name":  map[string]any{"type": "string"},
					"namespace": map[string]any{"type": "string"},
					"tail":      map[string]any{"type": "integer", "description": "Number of log lines (default: 100)"},
				},
			},
		},
		{
			Name:        "analyze_issue",
			Description: "Deep-dives into a specific issue ID with full AI analysis and remediation steps.",
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
			Description: "Returns the full SRE system prompt used by DevOpsGPT.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/mcp", s.handleMCP)
	s.mux.HandleFunc("/mcp/sse", s.handleSSE)
}

func (s *Server) Start(ctx context.Context) {
	srv := &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	s.cfg.Logger.Info("MCP server started", zap.String("port", s.cfg.Port))
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	srv.ListenAndServe()
}

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
			"serverInfo":      map[string]any{"name": "devopsgpt", "version": "1.0.0"},
			"instructions":    prompt.SRESystemPrompt,
		})
	case "tools/list":
		s.writeResult(w, req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		s.handleToolCall(w, r.Context(), req)
	default:
		s.writeError(w, req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) handleToolCall(w http.ResponseWriter, ctx context.Context, req MCPRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(w, req.ID, -32602, "Invalid params")
		return
	}

	var (
		result interface{}
		err    error
	)

	switch params.Name {
	case "get_cluster_issues":
		var args struct {
			Severity  string `json:"severity"`
			Namespace string `json:"namespace"`
		}
		json.Unmarshal(params.Arguments, &args)
		result, err = s.toolGetClusterIssues(args.Severity, args.Namespace)

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
		result = map[string]any{"system_prompt": prompt.SRESystemPrompt}

	default:
		s.writeError(w, req.ID, -32601, fmt.Sprintf("Tool not found: %s", params.Name))
		return
	}

	if err != nil {
		s.writeError(w, req.ID, -32603, err.Error())
		return
	}

	s.writeResult(w, req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": toJSON(result)}},
	})
}

func (s *Server) toolGetClusterIssues(severity, namespace string) (interface{}, error) {
	results := s.cfg.Watcher.GetResults()
	filtered := make([]map[string]any, 0)
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
	return map[string]any{"total": len(filtered), "issues": filtered}, nil
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
	if podName == "" || namespace == "" {
		return nil, fmt.Errorf("pod_name and namespace are required")
	}
	if tail <= 0 {
		tail = 100
	}
	tailLines := int64(tail)
	req := s.cfg.Watcher.K8sClient().CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &tailLines,
	})
	logs, err := req.DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs for %s/%s: %w", namespace, podName, err)
	}
	return map[string]any{
		"pod":       podName,
		"namespace": namespace,
		"tail":      tail,
		"logs":      string(logs),
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

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data, _ := json.Marshal(map[string]any{
		"type":         "capabilities",
		"tools":        s.tools,
		"instructions": prompt.SRESystemPrompt,
	})
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

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
