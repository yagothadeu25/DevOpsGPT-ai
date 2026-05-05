package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yagothadeu25/devopsgpt/pkg/analyzer"
	"github.com/yagothadeu25/devopsgpt/pkg/metrics"
	"go.uber.org/zap"
)

const (
	maxRetries     = 3
	retryBaseDelay = 2 * time.Second
)

type Notifier interface {
	Send(ctx context.Context, result *analyzer.Result) error
}

// ── Multi notifier ────────────────────────────────────────────────────────────

type multiNotifier struct {
	notifiers []Notifier
	log       *zap.Logger
}

func NewMultiNotifier(log *zap.Logger, n ...Notifier) Notifier {
	var valid []Notifier
	for _, notifier := range n {
		if notifier != nil {
			valid = append(valid, notifier)
		}
	}
	return &multiNotifier{notifiers: valid, log: log}
}

func (m *multiNotifier) Send(ctx context.Context, result *analyzer.Result) error {
	for _, n := range m.notifiers {
		if err := n.Send(ctx, result); err != nil {
			m.log.Error("notifier error", zap.Error(err))
		}
	}
	return nil
}

// ── Slack ─────────────────────────────────────────────────────────────────────

type slackNotifier struct {
	webhookURL string
	log        *zap.Logger
}

func NewSlack(webhookURL string, log *zap.Logger) Notifier {
	if webhookURL == "" {
		return nil
	}
	return &slackNotifier{webhookURL, log}
}

func (s *slackNotifier) Send(ctx context.Context, result *analyzer.Result) error {
	issue := result.Issue
	emoji := severityEmoji(string(issue.Severity))
	color := severityColor(string(issue.Severity))

	payload := map[string]any{
		"attachments": []map[string]any{
			{
				"color": color,
				"blocks": []map[string]any{
					{"type": "header", "text": map[string]any{"type": "plain_text", "text": fmt.Sprintf("%s DevOpsGPT Alert — %s", emoji, strings.ToUpper(string(issue.Severity)))}},
					{"type": "section", "fields": []map[string]any{
						{"type": "mrkdwn", "text": fmt.Sprintf("*Resource:*\n`%s/%s`", issue.Kind, issue.Name)},
						{"type": "mrkdwn", "text": fmt.Sprintf("*Namespace:*\n`%s`", issue.Namespace)},
					}},
					{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*Error:*\n```%s```", issue.Error)}},
					{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*AI Analysis:*\n%s", truncate(result.Analysis, 500))}},
					{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": fmt.Sprintf("DevOpsGPT · LLM: %s · %s", result.LLMUsed, result.ScannedAt.Format("15:04:05"))}}},
				},
			},
		},
	}

	err := sendWithRetry(ctx, s.webhookURL, payload, s.log)
	status := "success"
	if err != nil {
		status = "error"
	}
	metrics.NotificationsTotal.WithLabelValues("slack", status).Inc()
	return err
}

// ── Microsoft Teams ───────────────────────────────────────────────────────────

type teamsNotifier struct {
	webhookURL string
	log        *zap.Logger
}

func NewTeams(webhookURL string, log *zap.Logger) Notifier {
	if webhookURL == "" {
		return nil
	}
	return &teamsNotifier{webhookURL, log}
}

func (t *teamsNotifier) Send(ctx context.Context, result *analyzer.Result) error {
	issue := result.Issue
	emoji := severityEmoji(string(issue.Severity))

	payload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.4",
					"body": []map[string]any{
						{"type": "TextBlock", "size": "Large", "weight": "Bolder", "text": fmt.Sprintf("%s DevOpsGPT — %s", emoji, strings.ToUpper(string(issue.Severity))), "color": teamsColor(string(issue.Severity))},
						{"type": "FactSet", "facts": []map[string]any{
							{"title": "Resource", "value": fmt.Sprintf("%s/%s", issue.Kind, issue.Name)},
							{"title": "Namespace", "value": issue.Namespace},
							{"title": "LLM Used", "value": result.LLMUsed},
						}},
						{"type": "TextBlock", "text": fmt.Sprintf("**Error:** %s", issue.Error), "wrap": true},
						{"type": "TextBlock", "text": fmt.Sprintf("**Analysis:** %s", truncate(result.Analysis, 400)), "wrap": true},
					},
				},
			},
		},
	}

	err := sendWithRetry(ctx, t.webhookURL, payload, t.log)
	status := "success"
	if err != nil {
		status = "error"
	}
	metrics.NotificationsTotal.WithLabelValues("teams", status).Inc()
	return err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func sendWithRetry(ctx context.Context, url string, payload any, log *zap.Logger) error {
	b, _ := json.Marshal(payload)
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("webhook returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if i < maxRetries-1 {
			delay := retryBaseDelay * time.Duration(i+1)
			log.Warn("retrying webhook", zap.Int("attempt", i+1), zap.Error(lastErr))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}

func severityEmoji(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "error":
		return "🟠"
	case "warning":
		return "🟡"
	default:
		return "🔵"
	}
}

func severityColor(sev string) string {
	switch sev {
	case "critical", "error":
		return "#ef4444"
	case "warning":
		return "#eab308"
	default:
		return "#60a5fa"
	}
}

func teamsColor(sev string) string {
	switch sev {
	case "critical", "error":
		return "attention"
	case "warning":
		return "warning"
	default:
		return "accent"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
