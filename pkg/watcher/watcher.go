package watcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/analyzer"
	"github.com/yagothadeu25/devopsgpt/pkg/llm"
	"github.com/yagothadeu25/devopsgpt/pkg/metrics"
	"github.com/yagothadeu25/devopsgpt/pkg/notify"
	prompt_pkg "github.com/yagothadeu25/devopsgpt/pkg/prompt"
	"github.com/yagothadeu25/devopsgpt/pkg/remediation"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	llmTimeout     = 30 * time.Second
	maxRetries     = 3
	retryBaseDelay = 2 * time.Second
	maxResults     = 500
)

type Config struct {
	AllNamespaces bool
	PollInterval  time.Duration
	Registry      *analyzer.Registry
	LLMClient     llm.Client
	Notifier      notify.Notifier
	Remediator    *remediation.Remediator
	Logger        *zap.Logger
}

type Watcher struct {
	cfg     Config
	k8s     kubernetes.Interface
	mu      sync.RWMutex
	cache   map[string]*analyzer.Issue
	results []*analyzer.Result
}

func New(cfg Config) (*Watcher, error) {
	k8sClient, err := newK8sClient()
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}
	return &Watcher{
		cfg:   cfg,
		k8s:   k8sClient,
		cache: make(map[string]*analyzer.Issue),
	}, nil
}

func newK8sClient() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, err
		}
	}
	return kubernetes.NewForConfig(cfg)
}

func (w *Watcher) Run(ctx context.Context) {
	w.cfg.Logger.Info("watcher started", zap.Duration("interval", w.cfg.PollInterval))
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	w.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info("watcher stopped")
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

func (w *Watcher) scan(ctx context.Context) {
	w.cfg.Logger.Info("scanning all namespaces")
	metrics.ScansTotal.Inc()

	namespaces, err := w.getNamespaces(ctx)
	if err != nil {
		w.cfg.Logger.Error("failed to list namespaces", zap.Error(err))
		return
	}

	var allIssues []*analyzer.Issue
	for _, ns := range namespaces {
		issues, err := w.cfg.Registry.RunAll(ctx, w.k8s, ns)
		if err != nil {
			w.cfg.Logger.Error("analyzer error", zap.String("namespace", ns), zap.Error(err))
			continue
		}
		allIssues = append(allIssues, issues...)
	}

	// Update active issues gauge
	sevCount := map[string]float64{"critical": 0, "error": 0, "warning": 0, "info": 0}
	for _, i := range allIssues {
		sevCount[string(i.Severity)]++
	}
	for sev, count := range sevCount {
		metrics.ActiveIssues.WithLabelValues(sev).Set(count)
	}

	w.mu.Lock()
	var newIssues []*analyzer.Issue
	for _, issue := range allIssues {
		if _, seen := w.cache[issue.ID]; !seen {
			newIssues = append(newIssues, issue)
			w.cache[issue.ID] = issue
			metrics.IssuesDetected.WithLabelValues(string(issue.Severity), issue.Namespace, issue.Kind).Inc()
		}
	}

	// Clear resolved issues from cache
	activeIDs := make(map[string]bool, len(allIssues))
	for _, i := range allIssues {
		activeIDs[i.ID] = true
	}
	for id := range w.cache {
		if !activeIDs[id] {
			delete(w.cache, id)
		}
	}
	w.mu.Unlock()

	if len(newIssues) == 0 {
		w.cfg.Logger.Info("scan complete — no new issues", zap.Int("total", len(allIssues)))
		return
	}

	w.cfg.Logger.Info("new issues found", zap.Int("count", len(newIssues)))

	for _, issue := range newIssues {
		result, err := w.analyzeWithLLM(ctx, issue)
		if err != nil {
			w.cfg.Logger.Error("LLM analysis failed", zap.String("issue", issue.ID), zap.Error(err))
			continue
		}

		w.mu.Lock()
		if len(w.results) >= maxResults {
			w.results = w.results[len(w.results)-maxResults+1:]
		}
		w.results = append(w.results, result)
		w.mu.Unlock()

		if err := w.withRetry(ctx, "notify", func() error {
			return w.cfg.Notifier.Send(ctx, result)
		}); err != nil {
			w.cfg.Logger.Error("notification failed after retries", zap.Error(err))
		}

		if err := w.cfg.Remediator.Remediate(ctx, result); err != nil {
			w.cfg.Logger.Error("remediation failed", zap.Error(err))
		}
	}
}

func (w *Watcher) analyzeWithLLM(ctx context.Context, issue *analyzer.Issue) (*analyzer.Result, error) {
	llmCtx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()

	start := time.Now()
	provider := string(w.cfg.LLMClient.Provider())

	msg := fmt.Sprintf(
		"Analyze this Kubernetes issue:\n\nResource: %s/%s in namespace %s\nError: %s\nRaw data: %s",
		issue.Kind, issue.Name, issue.Namespace, issue.Error, issue.RawData,
	)

	var resp *llm.Response
	err := w.withRetry(llmCtx, "llm", func() error {
		var e error
		resp, e = w.cfg.LLMClient.Complete(llmCtx, prompt_pkg.SRESystemPrompt, []llm.Message{
			{Role: "user", Content: msg},
		})
		return e
	})

	duration := time.Since(start).Seconds()
	status := "success"
	if err != nil {
		status = "error"
		metrics.LLMRequestDuration.WithLabelValues(provider, status).Observe(duration)
		metrics.LLMRequestsTotal.WithLabelValues(provider, status).Inc()
		return nil, err
	}

	metrics.LLMRequestDuration.WithLabelValues(provider, status).Observe(duration)
	metrics.LLMRequestsTotal.WithLabelValues(provider, status).Inc()

	return &analyzer.Result{
		Issue:     issue,
		Analysis:  resp.Content,
		LLMUsed:   provider,
		ScannedAt: time.Now(),
	}, nil
}

func (w *Watcher) withRetry(ctx context.Context, op string, fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < maxRetries-1 {
			delay := retryBaseDelay * time.Duration(i+1)
			w.cfg.Logger.Warn("retrying operation",
				zap.String("op", op),
				zap.Int("attempt", i+1),
				zap.Duration("delay", delay),
				zap.Error(err),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("%s failed after %d retries: %w", op, maxRetries, err)
}

func (w *Watcher) getNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := w.k8s.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	return names, nil
}

func (w *Watcher) GetResults() []*analyzer.Result {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]*analyzer.Result, len(w.results))
	copy(out, w.results)
	return out
}

func (w *Watcher) K8sClient() kubernetes.Interface {
	return w.k8s
}
