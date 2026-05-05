package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ScansTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "devopsgpt_scans_total",
		Help: "Total number of cluster scans performed",
	})

	IssuesDetected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "devopsgpt_issues_detected_total",
		Help: "Total issues detected by severity and namespace",
	}, []string{"severity", "namespace", "kind"})

	LLMRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "devopsgpt_llm_request_duration_seconds",
		Help:    "LLM request latency in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "status"})

	LLMRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "devopsgpt_llm_requests_total",
		Help: "Total LLM requests by provider and status",
	}, []string{"provider", "status"})

	NotificationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "devopsgpt_notifications_total",
		Help: "Total notifications sent by channel and status",
	}, []string{"channel", "status"})

	ActiveIssues = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "devopsgpt_active_issues",
		Help: "Current number of active issues by severity",
	}, []string{"severity"})

	RemediationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "devopsgpt_remediations_total",
		Help: "Total auto-remediations by status",
	}, []string{"status", "dry_run"})
)
