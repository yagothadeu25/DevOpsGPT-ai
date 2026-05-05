package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/analyzer"
	"github.com/yagothadeu25/devopsgpt/pkg/llm"
	"github.com/yagothadeu25/devopsgpt/pkg/notify"
	"github.com/yagothadeu25/devopsgpt/pkg/remediation"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// SRE system prompt — the "brain" of DevOpsGPT
const sreSystemPrompt = `You are DevOpsGPT, an expert SRE and Kubernetes specialist with deep knowledge of:
- Kubernetes internals, pod lifecycle, networking, storage
- HTTP error patterns (401, 404, 500, 503, timeouts, connection resets)
- Fintech and credit platform reliability patterns
- Security and compliance best practices

When analyzing issues:
1. Identify the ROOT CAUSE clearly
2. Assess the SEVERITY (critical/error/warning/info)
3. Provide STEP-BY-STEP remediation commands
4. Estimate RISK of auto-remediation (low/medium/high)
5. Suggest PREVENTIVE measures

Always respond in JSON format:
{
  "root_cause": "...",
  "severity": "critical|error|warning|info",
  "explanation": "...",
  "remediation_steps": ["kubectl ...", "..."],
  "auto_remediation_risk": "low|medium|high",
  "auto_remediation_cmd": "kubectl ...",
  "prevention": "..."
}`

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
	cfg    Config
	k8s    kubernetes.Interface
	cache  map[string]*analyzer.Issue // issue ID → last seen
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
	// Try in-cluster first (when running as pod), then fallback to kubeconfig
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, err
		}
	}
	return kubernetes.NewForConfig(cfg)
}

// Run starts the main watch loop
func (w *Watcher) Run(ctx context.Context) {
	w.cfg.Logger.Info("watcher started", zap.Duration("interval", w.cfg.PollInterval))
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	// Run immediately on start
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

	// Enrich new/changed issues with LLM analysis
	var newIssues []*analyzer.Issue
	for _, issue := range allIssues {
		if _, seen := w.cache[issue.ID]; !seen {
			newIssues = append(newIssues, issue)
			w.cache[issue.ID] = issue
		}
	}

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

		w.results = append(w.results, result)

		// Notify Slack/Teams
		if err := w.cfg.Notifier.Send(ctx, result); err != nil {
			w.cfg.Logger.Error("notification failed", zap.Error(err))
		}

		// Auto-remediate if risk is acceptable
		if err := w.cfg.Remediator.Remediate(ctx, result); err != nil {
			w.cfg.Logger.Error("remediation failed", zap.Error(err))
		}
	}

	// Clear resolved issues from cache
	activeIDs := make(map[string]bool)
	for _, i := range allIssues {
		activeIDs[i.ID] = true
	}
	for id := range w.cache {
		if !activeIDs[id] {
			delete(w.cache, id)
		}
	}
}

func (w *Watcher) analyzeWithLLM(ctx context.Context, issue *analyzer.Issue) (*analyzer.Result, error) {
	prompt := fmt.Sprintf(
		"Analyze this Kubernetes issue:\n\nResource: %s/%s in namespace %s\nError: %s\nRaw data: %s",
		issue.Kind, issue.Name, issue.Namespace, issue.Error, issue.RawData,
	)

	resp, err := w.cfg.LLMClient.Complete(ctx, sreSystemPrompt, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	return &analyzer.Result{
		Issue:    issue,
		Analysis: resp.Content,
		LLMUsed:  string(w.cfg.LLMClient.Provider()),
		ScannedAt: time.Now(),
	}, nil
}

func (w *Watcher) getNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := w.k8s.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	return names, nil
}

// GetResults returns current scan results (used by REST API and MCP)
func (w *Watcher) GetResults() []*analyzer.Result { return w.results }
