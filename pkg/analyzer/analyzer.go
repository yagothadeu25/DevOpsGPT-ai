package analyzer

import (
	"context"
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type Issue struct {
	ID          string
	Kind        string
	Name        string
	Namespace   string
	Error       string
	RawData     string
	Severity    Severity
	TriggeredBy []string // e.g. ["HTTP_401", "TIMEOUT"]
	DetectedAt  time.Time
}

type Result struct {
	Issue     *Issue
	Analysis  string // LLM response JSON
	LLMUsed   string
	ScannedAt time.Time
}

type Analyzer interface {
	Name() string
	Run(ctx context.Context, k8s kubernetes.Interface, namespace string) ([]*Issue, error)
}

// ── Registry ──────────────────────────────────────────────────────────────────

type Registry struct {
	analyzers []Analyzer
}

func NewRegistry(analyzers ...Analyzer) *Registry {
	return &Registry{analyzers: analyzers}
}

func (r *Registry) RunAll(ctx context.Context, k8s kubernetes.Interface, namespace string) ([]*Issue, error) {
	var all []*Issue
	for _, a := range r.analyzers {
		issues, err := a.Run(ctx, k8s, namespace)
		if err != nil {
			continue // log externally
		}
		all = append(all, issues...)
	}
	return all, nil
}

// ── Pod Analyzer ──────────────────────────────────────────────────────────────

type PodAnalyzer struct{}

func NewPodAnalyzer() *PodAnalyzer { return &PodAnalyzer{} }
func (a *PodAnalyzer) Name() string { return "PodAnalyzer" }

func (a *PodAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	pods, err := k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			// CrashLoopBackOff
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				issues = append(issues, &Issue{
					ID:        fmt.Sprintf("pod-%s-%s-crashloop", ns, pod.Name),
					Kind:      "Pod",
					Name:      pod.Name,
					Namespace: ns,
					Error:     fmt.Sprintf("Container %s in CrashLoopBackOff (restarts: %d)", cs.Name, cs.RestartCount),
					Severity:  SeverityCritical,
					DetectedAt: time.Now(),
				})
			}
			// OOMKilled
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				issues = append(issues, &Issue{
					ID:        fmt.Sprintf("pod-%s-%s-oom", ns, pod.Name),
					Kind:      "Pod",
					Name:      pod.Name,
					Namespace: ns,
					Error:     fmt.Sprintf("Container %s OOMKilled — exceeded memory limit", cs.Name),
					Severity:  SeverityCritical,
					DetectedAt: time.Now(),
				})
			}
		}
	}
	return issues, nil
}

// ── HTTP Error Analyzer ───────────────────────────────────────────────────────
// Scans pod logs for HTTP error patterns

var httpPatterns = []struct {
	code    string
	pattern *regexp.Regexp
	sev     Severity
}{
	{"HTTP_401", regexp.MustCompile(`\b401\b|unauthorized`), SeverityError},
	{"HTTP_404", regexp.MustCompile(`\b404\b|not.?found`), SeverityWarning},
	{"HTTP_500", regexp.MustCompile(`\b500\b|internal.?server.?error`), SeverityCritical},
	{"HTTP_503", regexp.MustCompile(`\b503\b|service.?unavailable`), SeverityCritical},
	{"TIMEOUT",  regexp.MustCompile(`timeout|timed.?out|deadline.?exceeded`), SeverityError},
	{"CONNRESET",regexp.MustCompile(`connection.?refused|econnrefused|connection.?reset`), SeverityError},
}

type HTTPErrorAnalyzer struct{}

func NewHTTPErrorAnalyzer() *HTTPErrorAnalyzer { return &HTTPErrorAnalyzer{} }
func (a *HTTPErrorAnalyzer) Name() string       { return "HTTPErrorAnalyzer" }

func (a *HTTPErrorAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	pods, err := k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, pod := range pods.Items {
		// Fetch last 100 lines of logs
		tailLines := int64(100)
		req := k8s.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{TailLines: &tailLines})
		logs, err := req.DoRaw(ctx)
		if err != nil {
			continue
		}

		logStr := string(logs)
		var triggered []string
		for _, p := range httpPatterns {
			if p.pattern.MatchString(logStr) {
				triggered = append(triggered, p.code)
			}
		}

		if len(triggered) > 0 {
			issues = append(issues, &Issue{
				ID:          fmt.Sprintf("http-%s-%s", ns, pod.Name),
				Kind:        "Pod",
				Name:        pod.Name,
				Namespace:   ns,
				Error:       fmt.Sprintf("HTTP errors detected in logs: %v", triggered),
				RawData:     logStr[max(0, len(logStr)-500):], // last 500 chars
				Severity:    highestSeverity(triggered),
				TriggeredBy: triggered,
				DetectedAt:  time.Now(),
			})
		}
	}
	return issues, nil
}

// ── Service Analyzer ──────────────────────────────────────────────────────────

type ServiceAnalyzer struct{}

func NewServiceAnalyzer() *ServiceAnalyzer { return &ServiceAnalyzer{} }
func (a *ServiceAnalyzer) Name() string     { return "ServiceAnalyzer" }

func (a *ServiceAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	svcs, err := k8s.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, svc := range svcs.Items {
		if svc.Spec.Type == "ClusterIP" || svc.Spec.Type == "" {
			eps, err := k8s.CoreV1().Endpoints(ns).Get(ctx, svc.Name, metav1.GetOptions{})
			if err != nil || len(eps.Subsets) == 0 {
				issues = append(issues, &Issue{
					ID:        fmt.Sprintf("svc-%s-%s-no-endpoints", ns, svc.Name),
					Kind:      "Service",
					Name:      svc.Name,
					Namespace: ns,
					Error:     "Service has no ready endpoints — selector may not match any pods",
					Severity:  SeverityError,
					DetectedAt: time.Now(),
				})
			}
		}
	}
	return issues, nil
}

// ── HPA Analyzer ─────────────────────────────────────────────────────────────

type HPAAnalyzer struct{}

func NewHPAAnalyzer() *HPAAnalyzer { return &HPAAnalyzer{} }
func (a *HPAAnalyzer) Name() string { return "HPAAnalyzer" }

func (a *HPAAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	hpas, err := k8s.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, hpa := range hpas.Items {
		for _, cond := range hpa.Status.Conditions {
			if cond.Type == "AbleToScale" && cond.Status == "False" {
				issues = append(issues, &Issue{
					ID:        fmt.Sprintf("hpa-%s-%s", ns, hpa.Name),
					Kind:      "HorizontalPodAutoscaler",
					Name:      hpa.Name,
					Namespace: ns,
					Error:     fmt.Sprintf("HPA unable to scale: %s", cond.Message),
					Severity:  SeverityWarning,
					DetectedAt: time.Now(),
				})
			}
		}
	}
	return issues, nil
}

// ── PVC Analyzer ──────────────────────────────────────────────────────────────

type PVCAnalyzer struct{}

func NewPVCAnalyzer() *PVCAnalyzer { return &PVCAnalyzer{} }
func (a *PVCAnalyzer) Name() string { return "PVCAnalyzer" }

func (a *PVCAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	pvcs, err := k8s.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, pvc := range pvcs.Items {
		if pvc.Status.Phase == "Pending" {
			issues = append(issues, &Issue{
				ID:        fmt.Sprintf("pvc-%s-%s-pending", ns, pvc.Name),
				Kind:      "PersistentVolumeClaim",
				Name:      pvc.Name,
				Namespace: ns,
				Error:     "PVC stuck in Pending — no matching PersistentVolume found",
				Severity:  SeverityWarning,
				DetectedAt: time.Now(),
			})
		}
	}
	return issues, nil
}

// ── Deployment Analyzer ───────────────────────────────────────────────────────

type DeploymentAnalyzer struct{}

func NewDeploymentAnalyzer() *DeploymentAnalyzer { return &DeploymentAnalyzer{} }
func (a *DeploymentAnalyzer) Name() string        { return "DeploymentAnalyzer" }

func (a *DeploymentAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	deps, err := k8s.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, dep := range deps.Items {
		if dep.Status.UnavailableReplicas > 0 {
			issues = append(issues, &Issue{
				ID:        fmt.Sprintf("dep-%s-%s-unavailable", ns, dep.Name),
				Kind:      "Deployment",
				Name:      dep.Name,
				Namespace: ns,
				Error:     fmt.Sprintf("%d unavailable replicas (desired: %d, ready: %d)", dep.Status.UnavailableReplicas, *dep.Spec.Replicas, dep.Status.ReadyReplicas),
				Severity:  SeverityError,
				DetectedAt: time.Now(),
			})
		}
	}
	return issues, nil
}

// ── Node Analyzer ─────────────────────────────────────────────────────────────

type NodeAnalyzer struct{}

func NewNodeAnalyzer() *NodeAnalyzer { return &NodeAnalyzer{} }
func (a *NodeAnalyzer) Name() string  { return "NodeAnalyzer" }

func (a *NodeAnalyzer) Run(ctx context.Context, k8s kubernetes.Interface, ns string) ([]*Issue, error) {
	if ns != "" {
		return nil, nil // nodes are cluster-scoped, only run once
	}
	nodes, err := k8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var issues []*Issue
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			if cond.Type != "Ready" && cond.Status == "True" {
				issues = append(issues, &Issue{
					ID:        fmt.Sprintf("node-%s-%s", node.Name, cond.Type),
					Kind:      "Node",
					Name:      node.Name,
					Namespace: "cluster",
					Error:     fmt.Sprintf("Node condition %s=True: %s", cond.Type, cond.Message),
					Severity:  SeverityCritical,
					DetectedAt: time.Now(),
				})
			}
		}
	}
	return issues, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func highestSeverity(codes []string) Severity {
	for _, c := range codes {
		if c == "HTTP_500" || c == "HTTP_503" {
			return SeverityCritical
		}
	}
	for _, c := range codes {
		if c == "HTTP_401" || c == "TIMEOUT" || c == "CONNRESET" {
			return SeverityError
		}
	}
	return SeverityWarning
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}


