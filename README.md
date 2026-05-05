# DevOpsGPT

> Kubernetes AI Operator — monitora todas as namespaces, detecta erros HTTP em tempo real, analisa com LLM e notifica Slack/Teams automaticamente.

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                        │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │           DevOpsGPT Pod  (ns: devopsgpt)             │  │
│  │                                                      │  │
│  │  Watcher ──▶ Analyzers ──▶ LLM Client               │  │
│  │  (all ns)    Pod/Svc/HPA   Claude | Ollama           │  │
│  │  60s poll    PVC/Deploy    OpenAI | Bedrock          │  │
│  │              HTTPError                               │  │
│  │              Node                                    │  │
│  │                    │                                 │  │
│  │        ┌───────────┼──────────────┐                 │  │
│  │        ▼           ▼              ▼                  │  │
│  │     Slack        Teams        REST API  MCP Server   │  │
│  │     Webhook      Webhook      :8080     :8089        │  │
│  └──────────────────────────────────────────────────────┘  │
│                          │              │                    │
│                   React Dashboard  Claude Desktop           │
└─────────────────────────────────────────────────────────────┘
```

---

## O que é

DevOpsGPT é um operador Kubernetes escrito em Go, inspirado no [K8sGPT](https://k8sgpt.ai) (CNCF Sandbox), mas construído do zero com suporte a múltiplos backends de LLM, auto-remediation e integração nativa com Slack, Teams e Claude Desktop via MCP.

Diferente do K8sGPT que roda via CLI, o DevOpsGPT **vive dentro do cluster** como um pod com acesso RBAC somente-leitura em todas as namespaces, rodando continuamente e agindo como um SRE especialista 24/7.

---

## Features

- **All-namespace monitoring** — escaneia todos os namespaces automaticamente a cada intervalo configurável
- **HTTP error detection** — detecta `401`, `404`, `500`, `503`, timeouts e connection resets nos logs dos pods
- **Multi-LLM** — suporte a Claude, Ollama (local), OpenAI e AWS Bedrock via interface unificada
- **SRE system prompt** — análise estruturada com causa raiz, ação imediata, fix permanente e prevenção
- **Slack + Teams** — notificações simultâneas com Adaptive Cards e formatação de severidade
- **Auto-remediation** — engine com risk threshold configurável (`low/medium/high`), dry-run por padrão
- **REST API** — compatível com o formato do K8sGPT (`POST /v1/analyze`)
- **MCP Server** — integração com Claude Desktop, com SRE prompt enviado automaticamente no handshake
- **React Dashboard** — frontend com aba de providers, watcher automático e viewer do SRE prompt

---

## Estrutura do projeto

```
devopsgpt/
├── cmd/
│   └── devopsgpt/
│       └── main.go                  # Entry point — orquestra todos os componentes
├── pkg/
│   ├── analyzer/
│   │   └── analyzer.go              # Pod, Service, HPA, PVC, Deployment, Node, HTTPError
│   ├── llm/
│   │   └── client.go                # Claude, Ollama, OpenAI, Bedrock (interface unificada)
│   ├── watcher/
│   │   └── watcher.go               # Watch loop + LLM enrichment
│   ├── notify/
│   │   └── notifier.go              # Slack + Teams
│   ├── remediation/
│   │   └── remediation.go           # Auto-fix com risk threshold
│   ├── mcp/
│   │   └── server.go                # MCP Server JSON-RPC 2.0 (:8089)
│   └── server/
│       └── server.go                # REST API (:8080)
├── deploy/
│   └── manifests.yaml               # Namespace, RBAC, Secret, ConfigMap, Deployment, SVC, HPA
├── dashboard/
│   └── k8sgpt-dashboard.jsx         # React frontend com Provider selector + SRE Prompt viewer
├── Dockerfile                       # Multi-stage, distroless
├── go.mod
└── README.md
```

---

## Analyzers disponíveis

| Analyzer | O que detecta |
|---|---|
| `PodAnalyzer` | CrashLoopBackOff, OOMKilled |
| `HTTPErrorAnalyzer` | 401, 404, 500, 503, timeout, ECONNREFUSED nos logs |
| `ServiceAnalyzer` | Services sem endpoints |
| `HPAAnalyzer` | HPAs incapazes de escalar |
| `PVCAnalyzer` | PVCs em estado Pending |
| `DeploymentAnalyzer` | Deployments com réplicas indisponíveis |
| `NodeAnalyzer` | Condições anormais nos nodes |

---

## LLM Backends

| Provider | Config | Local | Requer key |
|---|---|---|---|
| Claude (Anthropic) | `LLM_PROVIDER=claude` | ❌ | ✅ |
| Ollama | `LLM_PROVIDER=ollama` | ✅ | ❌ |
| OpenAI | `LLM_PROVIDER=openai` | ❌ | ✅ |
| AWS Bedrock | `LLM_PROVIDER=bedrock` | ❌ | IAM Role |

Para ambientes com requisitos de compliance (PCI-DSS, SOC2), recomenda-se Ollama com Llama 3 — zero dado sensível sai do cluster.

---

## Quickstart

### 1. Preencher secrets

```bash
# Editar deploy/manifests.yaml — seção Secret
vim deploy/manifests.yaml
```

```yaml
stringData:
  ANTHROPIC_API_KEY: "sk-ant-..."
  SLACK_WEBHOOK_URL:  "https://hooks.slack.com/services/..."
  TEAMS_WEBHOOK_URL:  "https://..."
```

### 2. Deploy no cluster

```bash
kubectl apply -f deploy/manifests.yaml
kubectl get pods -n devopsgpt -w
```

### 3. Verificar logs

```bash
kubectl logs -n devopsgpt -l app=devopsgpt -f
```

### 4. Acessar APIs

```bash
# REST API
kubectl port-forward svc/devopsgpt-api 8080:8080 -n devopsgpt
curl http://localhost:8080/v1/results | jq

# MCP Server (Claude Desktop)
kubectl port-forward svc/devopsgpt-api 8089:8089 -n devopsgpt
```

### 5. Build e push da imagem

```bash
docker build -t yourusername/devopsgpt:latest .
docker push yourusername/devopsgpt:latest

# Atualizar image no manifests.yaml e reaplicar
kubectl rollout restart deploy/devopsgpt -n devopsgpt
```

---

## Variáveis de ambiente

| Variável | Default | Descrição |
|---|---|---|
| `LLM_PROVIDER` | `claude` | `claude / ollama / openai / bedrock` |
| `LLM_MODEL` | `claude-sonnet-4-20250514` | Modelo a usar |
| `LLM_BASE_URL` | — | Base URL customizada (Ollama: `http://ollama:11434`) |
| `POLL_INTERVAL` | `60s` | Intervalo entre scans |
| `AUTO_REMEDIATE` | `false` | Habilitar auto-correção |
| `RISK_THRESHOLD` | `low` | Risco máximo aceito: `low / medium / high` |
| `DRY_RUN` | `true` | Simular sem aplicar comandos |
| `API_PORT` | `8080` | Porta REST API |
| `MCP_PORT` | `8089` | Porta MCP Server |
| `SLACK_WEBHOOK_URL` | — | Webhook Slack |
| `TEAMS_WEBHOOK_URL` | — | Webhook Microsoft Teams |

---

## REST API

| Método | Endpoint | Descrição |
|---|---|---|
| `GET` | `/healthz` | Health check |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/v1/results` | Todos os issues com AI analysis |
| `POST` | `/v1/analyze` | Trigger análise (compatível com K8sGPT) |
| `GET` | `/v1/summary` | Saúde por namespace |
| `GET` | `/v1/providers` | Providers LLM disponíveis |
| `GET` | `/v1/prompt` | SRE system prompt atual |

---

## MCP Server — Claude Desktop

O MCP Server expõe 5 tools para o Claude Desktop:

| Tool | Descrição |
|---|---|
| `get_cluster_issues` | Issues atuais com filtro por severity e namespace |
| `get_namespace_summary` | Saúde resumida por namespace |
| `get_pod_logs` | Logs recentes de um pod específico |
| `analyze_issue` | Deep-dive em um issue por ID |
| `get_sre_prompt` | Retorna o SRE system prompt |

### Configuração

```json
// ~/.config/claude/claude_desktop_config.json
{
  "mcpServers": {
    "devopsgpt": {
      "url": "http://localhost:8089/mcp"
    }
  }
}
```

O SRE system prompt é enviado automaticamente no handshake (`initialize.instructions`). O Claude Desktop age como SRE specialist em toda sessão sem configuração adicional.

### Exemplos de perguntas

```
Quais pods estão em CrashLoop na produção?
Por que o auth-gateway está retornando 401?
Resume o estado de saúde do cluster
Qual namespace tem mais erros hoje?
Sugira um rollback para o payment-api
```

---

## Auto-remediation

O engine de auto-remediation aplica comandos `kubectl` gerados pelo LLM, com as seguintes salvaguardas:

- Somente comandos iniciados com `kubectl` são aceitos
- Comandos destrutivos (`delete namespace`, `delete node`, `delete pv`) são bloqueados
- `RISK_THRESHOLD` controla o risco máximo aceito (`low / medium / high`)
- `DRY_RUN=true` (padrão) apenas loga o comando sem executar

Para habilitar com cautela:

```yaml
AUTO_REMEDIATE: "true"
RISK_THRESHOLD: "low"   # só aplica correções de baixo risco
DRY_RUN: "false"
```

---

## SRE System Prompt

O prompt padrão instrui o DevOpsGPT a responder sempre no formato:

```
🔴 SEVERITY: critical/error/warning/info
🎯 ROOT CAUSE: causa raiz concisa
⚡ IMMEDIATE ACTION: kubectl commands prontos
🔧 PERMANENT FIX: passos de correção
🛡️ PREVENTION: o que adicionar/mudar
```

O prompt é editável via aba **SRE Prompt** no dashboard React e via `GET /v1/prompt`.

---

## RBAC

O DevOpsGPT usa um `ClusterRole` com permissões **somente-leitura** em todos os recursos, exceto:

- `deployments` — `patch/update` (rollback)
- `pods` — `delete` (restart)

Nenhuma permissão de escrita em `secrets`, `namespaces`, `nodes` ou `persistentvolumes`.

---

## Roadmap

- [ ] Webhook receiver para eventos do Kubernetes (sem polling)
- [ ] Integração com Prometheus AlertManager
- [ ] PagerDuty / OpsGenie notifier
- [ ] UI de histórico de incidents
- [ ] Fine-tuning de modelo próprio com dados do cluster
- [ ] Helm chart

---

## Créditos

Inspirado no [K8sGPT](https://k8sgpt.ai) — projeto CNCF Sandbox.

Construído por [Yago Martins](https://desvsecops.com/blog) — DevSecOps/SRE Specialist.

---

*desvsecops.com/blog*
