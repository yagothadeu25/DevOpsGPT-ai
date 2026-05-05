package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/watcher"
	"go.uber.org/zap"
)

type Config struct {
	Port    string
	Watcher *watcher.Watcher
	Logger  *zap.Logger
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Start(ctx context.Context) {
	srv := &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	s.cfg.Logger.Info("REST API started", zap.String("port", s.cfg.Port))
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	srv.ListenAndServe()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz",      s.handleHealth)
	s.mux.HandleFunc("/readyz",       s.handleReady)
	s.mux.HandleFunc("/v1/results",   s.handleResults)
	s.mux.HandleFunc("/v1/analyze",   s.handleAnalyze)
	s.mux.HandleFunc("/v1/summary",   s.handleSummary)
	s.mux.HandleFunc("/v1/providers", s.handleProviders)
	s.mux.HandleFunc("/v1/prompt",    s.handlePrompt)
}

// GET /healthz
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]string{"status": "ok"})
}

// GET /readyz
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]string{"status": "ready"})
}

// GET /v1/results — returns all current scan results
func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	s.cors(w)
	results := s.cfg.Watcher.GetResults()
	var out []map[string]any
	for _, r := range results {
		out = append(out, map[string]any{
			"id":           r.Issue.ID,
			"kind":         r.Issue.Kind,
			"name":         r.Issue.Name,
			"namespace":    r.Issue.Namespace,
			"error":        r.Issue.Error,
			"severity":     r.Issue.Severity,
			"triggered_by": r.Issue.TriggeredBy,
			"details":      r.Analysis,
			"llm_used":     r.LLMUsed,
			"scanned_at":   r.ScannedAt.Format(time.RFC3339),
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	s.json(w, map[string]any{"results": out, "total": len(out)})
}

// POST /v1/analyze — compatible with k8sgpt API format
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	s.cors(w)
	if r.Method == http.MethodOptions {
		return
	}
	// Trigger a new scan (async) and return current results
	results := s.cfg.Watcher.GetResults()
	var out []map[string]any
	for _, r := range results {
		out = append(out, map[string]any{
			"name":      r.Issue.Name,
			"error":     []string{r.Issue.Error},
			"details":   r.Analysis,
			"kind":      r.Issue.Kind,
			"namespace": r.Issue.Namespace,
			"severity":  r.Issue.Severity,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	s.json(w, map[string]any{"results": out})
}

// GET /v1/summary — namespace health summary
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	s.cors(w)
	results := s.cfg.Watcher.GetResults()
	summary := map[string]map[string]int{}
	for _, r := range results {
		ns := r.Issue.Namespace
		if _, ok := summary[ns]; !ok {
			summary[ns] = map[string]int{"critical": 0, "error": 0, "warning": 0, "info": 0, "total": 0}
		}
		summary[ns][string(r.Issue.Severity)]++
		summary[ns]["total"]++
	}
	s.json(w, summary)
}

// GET /v1/providers — available LLM providers and current selection
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	s.cors(w)
	s.json(w, map[string]any{
		"providers": []map[string]any{
			{
				"id":          "claude",
				"name":        "Claude (Anthropic)",
				"models":      []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-haiku-4-5-20251001"},
				"requires_key": true,
				"local":       false,
			},
			{
				"id":          "ollama",
				"name":        "Ollama (Local)",
				"models":      []string{"llama3", "mistral", "codellama", "phi3", "gemma2"},
				"requires_key": false,
				"local":       true,
			},
			{
				"id":          "openai",
				"name":        "OpenAI",
				"models":      []string{"gpt-4o", "gpt-4o-mini", "gpt-4-turbo"},
				"requires_key": true,
				"local":       false,
			},
			{
				"id":          "bedrock",
				"name":        "AWS Bedrock",
				"models":      []string{"anthropic.claude-3-5-sonnet-20241022-v2:0", "amazon.titan-text-premier-v1:0"},
				"requires_key": false,
				"local":       false,
				"note":        "Uses AWS IAM credentials",
			},
		},
	})
}

// GET /v1/prompt — returns the SRE system prompt
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	s.cors(w)
	s.json(w, map[string]any{
		"role":   "DevOpsGPT — SRE Specialist",
		"prompt": "See /mcp for the full system prompt used by the MCP server",
	})
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}
