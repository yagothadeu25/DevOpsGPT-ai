package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yagothadeu25/devopsgpt/pkg/prompt"
	"github.com/yagothadeu25/devopsgpt/pkg/watcher"
	"go.uber.org/zap"
)

type Config struct {
	Port        string
	Watcher     *watcher.Watcher
	Logger      *zap.Logger
	APIToken    string // optional — if set, requires Authorization: Bearer <token>
	AllowOrigin string // CORS origin, defaults to http://localhost:3000
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func New(cfg Config) *Server {
	if cfg.AllowOrigin == "" {
		cfg.AllowOrigin = "http://localhost:3000"
	}
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
		IdleTimeout:  60 * time.Second,
	}
	s.cfg.Logger.Info("REST API started", zap.String("port", s.cfg.Port))
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	srv.ListenAndServe()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/readyz", s.handleReady)
	s.mux.Handle("/metrics", promhttp.Handler())
	s.mux.HandleFunc("/v1/results", s.auth(s.handleResults))
	s.mux.HandleFunc("/v1/analyze", s.auth(s.handleAnalyze))
	s.mux.HandleFunc("/v1/summary", s.auth(s.handleSummary))
	s.mux.HandleFunc("/v1/providers", s.auth(s.handleProviders))
	s.mux.HandleFunc("/v1/prompt", s.auth(s.handlePrompt))
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.cors(w, r)
		if r.Method == http.MethodOptions {
			return
		}
		if s.cfg.APIToken != "" {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token != s.cfg.APIToken {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]string{"status": "ready"})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	results := s.cfg.Watcher.GetResults()
	out := make([]map[string]any, 0, len(results))
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
	s.json(w, map[string]any{"results": out, "total": len(out)})
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	results := s.cfg.Watcher.GetResults()
	out := make([]map[string]any, 0, len(results))
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
	s.json(w, map[string]any{"results": out})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]any{
		"providers": []map[string]any{
			{"id": "claude", "name": "Claude (Anthropic)", "models": []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"}, "requires_key": true, "local": false},
			{"id": "ollama", "name": "Ollama (Local)", "models": []string{"llama3", "mistral", "codellama", "phi3"}, "requires_key": false, "local": true},
			{"id": "openai", "name": "OpenAI", "models": []string{"gpt-4o", "gpt-4o-mini", "gpt-4-turbo"}, "requires_key": true, "local": false},
			{"id": "bedrock", "name": "AWS Bedrock", "models": []string{"anthropic.claude-3-5-sonnet-20241022-v2:0"}, "requires_key": false, "local": false},
		},
	})
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	s.json(w, map[string]any{
		"role":   "DevOpsGPT — SRE Specialist",
		"prompt": prompt.SRESystemPrompt,
	})
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) cors(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == s.cfg.AllowOrigin || s.cfg.AllowOrigin == "*" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
