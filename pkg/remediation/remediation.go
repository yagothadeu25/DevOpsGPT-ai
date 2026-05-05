package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yagothadeu25/devopsgpt/pkg/analyzer"
	"go.uber.org/zap"
)

type Config struct {
	AutoApply     bool
	RiskThreshold string // "low" | "medium" | "high"
	DryRun        bool
	Logger        *zap.Logger
}

type Remediator struct {
	cfg Config
}

func New(cfg Config) *Remediator {
	return &Remediator{cfg: cfg}
}

// LLMAnalysis is the structured JSON the LLM returns
type LLMAnalysis struct {
	RootCause            string   `json:"root_cause"`
	Severity             string   `json:"severity"`
	Explanation          string   `json:"explanation"`
	RemediationSteps     []string `json:"remediation_steps"`
	AutoRemediationRisk  string   `json:"auto_remediation_risk"`  // low | medium | high
	AutoRemediationCmd   string   `json:"auto_remediation_cmd"`
	Prevention           string   `json:"prevention"`
}

func (r *Remediator) Remediate(ctx context.Context, result *analyzer.Result) error {
	if !r.cfg.AutoApply {
		return nil
	}

	var analysis LLMAnalysis
	if err := json.Unmarshal([]byte(result.Analysis), &analysis); err != nil {
		return fmt.Errorf("failed to parse LLM analysis: %w", err)
	}

	// Check if risk is within threshold
	if !r.riskAcceptable(analysis.AutoRemediationRisk) {
		r.cfg.Logger.Info("skipping auto-remediation — risk too high",
			zap.String("issue", result.Issue.ID),
			zap.String("risk", analysis.AutoRemediationRisk),
			zap.String("threshold", r.cfg.RiskThreshold),
		)
		return nil
	}

	if analysis.AutoRemediationCmd == "" {
		return nil
	}

	if r.cfg.DryRun {
		r.cfg.Logger.Info("DRY RUN — would execute",
			zap.String("cmd", analysis.AutoRemediationCmd),
			zap.String("issue", result.Issue.ID),
		)
		return nil
	}

	return r.execute(ctx, analysis.AutoRemediationCmd, result.Issue)
}

func (r *Remediator) execute(ctx context.Context, cmd string, issue *analyzer.Issue) error {
	// Safety: only allow kubectl commands
	if !strings.HasPrefix(strings.TrimSpace(cmd), "kubectl") {
		return fmt.Errorf("rejected non-kubectl command: %s", cmd)
	}

	// Additional safety: block destructive ops on production
	blocked := []string{"delete namespace", "delete node", "delete pv "}
	for _, b := range blocked {
		if strings.Contains(cmd, b) {
			return fmt.Errorf("blocked dangerous command: %s", cmd)
		}
	}

	r.cfg.Logger.Info("executing remediation",
		zap.String("cmd", cmd),
		zap.String("resource", fmt.Sprintf("%s/%s", issue.Kind, issue.Name)),
		zap.String("namespace", issue.Namespace),
	)

	parts := strings.Fields(cmd)
	out, err := exec.CommandContext(ctx, parts[0], parts[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("remediation failed: %w — output: %s", err, string(out))
	}

	r.cfg.Logger.Info("remediation applied",
		zap.String("output", string(out)),
	)
	return nil
}

func (r *Remediator) riskAcceptable(risk string) bool {
	levels := map[string]int{"low": 1, "medium": 2, "high": 3}
	return levels[risk] <= levels[r.cfg.RiskThreshold]
}
