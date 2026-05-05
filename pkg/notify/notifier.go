package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yagothadeu25/devopsgpt/pkg/analyzer"
)

type Notifier interface {
	Send(ctx context.Context, result *analyzer.Result) error
}

// ── Multi notifier ────────────────────────────────────────────────────────────

type multiNotifier struct{ notifiers []Notifier }

func NewMultiNotifier(n ...Notifier) Notifier {
	var valid []Notifier
	for _, notifier := range n {
		if notifier != nil {
			valid = append(valid, notifier)
		}
	}
	return &multiNotifier{notifiers: valid}
}

func (m *multiNotifier) Send(ctx context.Context, result *analyzer.Result) error {
	for _, n := range m.notifiers {
		if err := n.Send(ctx, result); err != nil {
			fmt.Printf("notifier error: %v\n", err)
		}
	}
	return nil
}

// ── Slack ─────────────────────────────────────────────────────────────────────

type slackNotifier struct{ webhookURL string }

func NewSlack(webhookURL string) Notifier {
	if webhookURL == "" {
		return nil
	}
	return &slackNotifier{webhookURL}
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
					{
						"type": "header",
						"text": map[string]any{
							"type": "plain_text",
							"text": fmt.Sprintf("%s DevOpsGPT Alert — %s", emoji, strings.ToUpper(string(issue.Severity))),
						},
					},
					{
						"type": "section",
						"fields": []map[string]any{
							{"type": "mrkdwn", "text": fmt.Sprintf("*Resource:*\n`%s/%s`", issue.Kind, issue.Name)},
							{"type": "mrkdwn", "text": fmt.Sprintf("*Namespace:*\n`%s`", issue.Namespace)},
						},
					},
					{
						"type": "section",
						"text": map[string]any{
							"type": "mrkdwn",
							"text": fmt.Sprintf("*Error:*\n```%s```", issue.Error),
						},
					},
					{
						"type": "section",
						"text": map[string]any{
							"type": "mrkdwn",
							"text": fmt.Sprintf("*AI Analysis:*\n%s", truncate(result.Analysis, 500)),
						},
					},
					{
						"type": "context",
						"elements": []map[string]any{
							{"type": "mrkdwn", "text": fmt.Sprintf("DevOpsGPT · LLM: %s · %s", result.LLMUsed, result.ScannedAt.Format("15:04:05"))},
						},
					},
				},
			},
		},
	}

	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned %d", resp.StatusCode)
	}
	return nil
}

// ── Microsoft Teams ───────────────────────────────────────────────────────────

type teamsNotifier struct{ webhookURL string }

func NewTeams(webhookURL string) Notifier {
	if webhookURL == "" {
		return nil
	}
	return &teamsNotifier{webhookURL}
}

func (t *teamsNotifier) Send(ctx context.Context, result *analyzer.Result) error {
	issue := result.Issue
	emoji := severityEmoji(string(issue.Severity))

	// Teams Adaptive Card format
	payload := map[string]any{
		"type":        "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.4",
					"body": []map[string]any{
						{
							"type":   "TextBlock",
							"size":   "Large",
							"weight": "Bolder",
							"text":   fmt.Sprintf("%s DevOpsGPT — %s", emoji, strings.ToUpper(string(issue.Severity))),
							"color":  teamsColor(string(issue.Severity)),
						},
						{
							"type": "FactSet",
							"facts": []map[string]any{
								{"title": "Resource", "value": fmt.Sprintf("%s/%s", issue.Kind, issue.Name)},
								{"title": "Namespace", "value": issue.Namespace},
								{"title": "LLM Used", "value": result.LLMUsed},
							},
						},
						{
							"type":  "TextBlock",
							"text":  fmt.Sprintf("**Error:** %s", issue.Error),
							"wrap":  true,
						},
						{
							"type":  "TextBlock",
							"text":  fmt.Sprintf("**Analysis:** %s", truncate(result.Analysis, 400)),
							"wrap":  true,
						},
					},
				},
			},
		},
	}

	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", t.webhookURL, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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
