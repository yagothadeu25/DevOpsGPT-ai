import { useState, useEffect, useRef, useCallback } from "react";

// ─── Palette ──────────────────────────────────────────────────────────────────
const C = {
  bg:"#080b0f", panel:"#0d1117", panel2:"#0a0d12",
  border:"#1a2332", borderHi:"#2a3a50",
  amber:"#f59e0b", amberDim:"#92600a", amberLo:"rgba(245,158,11,0.1)",
  green:"#22c55e", greenLo:"rgba(34,197,94,0.1)",
  red:"#ef4444", redLo:"rgba(239,68,68,0.1)",
  blue:"#60a5fa", blueLo:"rgba(96,165,250,0.1)",
  purple:"#a78bfa", purpleLo:"rgba(167,139,250,0.1)",
  yellow:"#eab308",
  muted:"#374151", text:"#94a3b8", textDim:"#4b5563", white:"#e2e8f0",
};

// ─── Providers config ─────────────────────────────────────────────────────────
const PROVIDERS = [
  {
    id:"claude", name:"Claude", vendor:"Anthropic", icon:"🧠", color:C.purple,
    local:false, requiresKey:true,
    models:["claude-sonnet-4-20250514","claude-opus-4-20250514","claude-haiku-4-5-20251001"],
    placeholder:"sk-ant-...",
    desc:"Melhor análise de causa raiz. Ideal para ambientes com requisitos de compliance.",
  },
  {
    id:"ollama", name:"Ollama", vendor:"Local", icon:"🏠", color:C.green,
    local:true, requiresKey:false,
    models:["llama3","mistral","codellama","phi3","gemma2"],
    placeholder:"http://localhost:11434",
    desc:"Roda localmente. Zero dado sensível sai do cluster. Ideal para produção.",
  },
  {
    id:"openai", name:"OpenAI", vendor:"OpenAI", icon:"✦", color:C.blue,
    local:false, requiresKey:true,
    models:["gpt-4o","gpt-4o-mini","gpt-4-turbo"],
    placeholder:"sk-...",
    desc:"Alta qualidade. Suporte nativo a function calling.",
  },
  {
    id:"bedrock", name:"Bedrock", vendor:"AWS", icon:"☁️", color:C.amber,
    local:false, requiresKey:false,
    models:["anthropic.claude-3-5-sonnet-20241022-v2:0","amazon.titan-text-premier-v1:0"],
    placeholder:"us-east-1",
    desc:"Usa credenciais IAM. Ideal para workloads já na AWS.",
  },
];

// ─── SRE Prompt ───────────────────────────────────────────────────────────────
const SRE_PROMPT = `You are DevOpsGPT, an expert SRE (Site Reliability Engineer) specialist
with deep expertise in Kubernetes troubleshooting, incident response, and platform engineering.

Your core competencies:
- Kubernetes internals: pod lifecycle, scheduling, networking (CNI, DNS, ingress), storage (PV/PVC)
- Observability: logs, metrics (Prometheus), traces, events
- HTTP error patterns: 401 (auth), 404 (routing), 500/503 (upstream failures), timeouts, connection resets
- Fintech reliability: high availability, zero-downtime deployments, data consistency
- Security: RBAC, NetworkPolicies, secrets management, compliance (PCI-DSS, SOC2)
- CI/CD: GitOps with ArgoCD, Helm, Kustomize
- Cloud: AWS (EKS, RDS, ElastiCache), GCP (GKE), Azure (AKS)

When troubleshooting, you always:
1. Ask clarifying questions if context is missing (namespace, pod name, error logs)
2. Follow the SRE golden signals: latency, traffic, errors, saturation
3. Think in layers: application → container → pod → node → network → infrastructure
4. Provide kubectl commands ready to copy-paste
5. Assess blast radius before suggesting any change
6. Distinguish between symptoms and root causes
7. Suggest both immediate mitigation AND long-term fix

Response format for incidents:
🔴 SEVERITY: [critical/error/warning]
🎯 ROOT CAUSE: [concise explanation]
⚡ IMMEDIATE ACTION: [kubectl commands]
🔧 PERMANENT FIX: [steps]
🛡️ PREVENTION: [what to add/change]

You have access to real-time cluster data via DevOpsGPT tools.
Always use the tools before answering questions about cluster state.`;

// ─── HTTP error patterns ──────────────────────────────────────────────────────
const WATCHED = [
  {code:"401",color:C.yellow,pattern:/\b401\b|unauthorized/i},
  {code:"404",color:C.blue,  pattern:/\b404\b|not.?found/i},
  {code:"500",color:C.red,   pattern:/\b500\b|internal.?server/i},
  {code:"503",color:C.red,   pattern:/\b503\b|unavailable/i},
  {code:"TOT",color:C.amber, pattern:/timeout|timed.?out|deadline/i},
  {code:"CON",color:C.amber, pattern:/econnrefused|connection.?reset/i},
];

const POLL_OPTS = [15,30,60,120,300];
const SEV = {
  critical:{color:C.red,   label:"CRITICAL"},
  error:   {color:C.red,   label:"ERROR"},
  warning: {color:C.yellow,label:"WARN"},
  info:    {color:C.blue,  label:"INFO"},
};
function getSev(s=""){ return SEV[s.toLowerCase()]||SEV.info; }

// ─── Mock ─────────────────────────────────────────────────────────────────────
const MOCK=[
  {id:"1",kind:"Pod",name:"payment-api-7d9f8b-xk2lp",namespace:"production",error:"CrashLoopBackOff — 500 Internal Server Error on /api/charge",details:"🔴 SEVERITY: critical\n🎯 ROOT CAUSE: Container OOMKilled after receiving HTTP 500 from billing service. Exit code 137.\n⚡ IMMEDIATE ACTION: kubectl rollout restart deploy/payment-api -n production\n🔧 PERMANENT FIX: Increase memory limit to 512Mi. Add circuit breaker to billing calls.\n🛡️ PREVENTION: Add resource quotas and HPA based on memory utilization.",severity:"critical"},
  {id:"2",kind:"Service",name:"auth-gateway",namespace:"production",error:"Repeated 401 Unauthorized — JWT validation failing across all replicas",details:"🔴 SEVERITY: critical\n🎯 ROOT CAUSE: JWT secret rotated but not propagated to all pods. Pods using stale secret.\n⚡ IMMEDIATE ACTION: kubectl rollout restart deploy/auth-gateway -n production\n🔧 PERMANENT FIX: Use ExternalSecrets with auto-reload. Avoid manual secret rotation.\n🛡️ PREVENTION: Implement secret versioning and zero-downtime rotation pipeline.",severity:"critical"},
  {id:"3",kind:"Pod",name:"fraud-detector-6c9b-p8xqm",namespace:"risk",error:"Connection timed out reaching ml-inference:8501 after 30s",details:"🔴 SEVERITY: error\n🎯 ROOT CAUSE: ML inference service under heavy load, not responding within timeout window.\n⚡ IMMEDIATE ACTION: kubectl top pods -n risk && kubectl scale deploy/ml-inference --replicas=3 -n risk\n🔧 PERMANENT FIX: Configure HPA for ml-inference based on request latency.\n🛡️ PREVENTION: Set up circuit breaker + fallback model for inference timeouts.",severity:"error"},
  {id:"4",kind:"Deployment",name:"notification-worker",namespace:"messaging",error:"HTTP 404 — endpoint /v2/send-push deprecated and removed",details:"🔴 SEVERITY: error\n🎯 ROOT CAUSE: notification-worker still calls /v2/send-push removed in last API version.\n⚡ IMMEDIATE ACTION: kubectl set env deploy/notification-worker PUSH_API_URL=/v3/notifications -n messaging\n🔧 PERMANENT FIX: Update ConfigMap notification-config and redeploy.\n🛡️ PREVENTION: API versioning strategy + contract tests in CI pipeline.",severity:"error"},
  {id:"5",kind:"Pod",name:"report-exporter",namespace:"data",error:"503 Service Unavailable — postgres-read-replica refusing connections",details:"🔴 SEVERITY: error\n🎯 ROOT CAUSE: Read replica failover event. Connection pool pointing to old instance.\n⚡ IMMEDIATE ACTION: kubectl rollout restart deploy/report-exporter -n data\n🔧 PERMANENT FIX: Use RDS Proxy to handle failover transparently.\n🛡️ PREVENTION: Health checks on DB connections. Implement retry with exponential backoff.",severity:"error"},
];

function tagIssues(list){
  return list.map(i=>({
    ...i,
    triggeredBy:WATCHED.filter(e=>e.pattern.test(`${i.error} ${i.details}`)).map(e=>e.code),
  }));
}

// ─── CSS ──────────────────────────────────────────────────────────────────────
const STYLES=`
  @import url('https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@300;400;500;700&family=Syne:wght@700;800&display=swap');
  *,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
  body{background:${C.bg};color:${C.text};font-family:'JetBrains Mono',monospace;min-height:100vh}
  body::before{content:'';position:fixed;inset:0;pointer-events:none;z-index:0;
    background-image:linear-gradient(rgba(245,158,11,0.015) 1px,transparent 1px),linear-gradient(90deg,rgba(245,158,11,0.015) 1px,transparent 1px);
    background-size:40px 40px}
  ::-webkit-scrollbar{width:3px}
  ::-webkit-scrollbar-thumb{background:${C.border};border-radius:2px}
  @keyframes pulse-g{0%,100%{box-shadow:0 0 0 0 rgba(34,197,94,.5)}50%{box-shadow:0 0 0 6px rgba(34,197,94,0)}}
  @keyframes pulse-r{0%,100%{box-shadow:0 0 0 0 rgba(239,68,68,.5)}50%{box-shadow:0 0 0 6px rgba(239,68,68,0)}}
  @keyframes slide-in{from{opacity:0;transform:translateY(10px)}to{opacity:1;transform:translateY(0)}}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:0}}
  @keyframes spin{to{transform:rotate(360deg)}}
  @keyframes flash-r{0%,100%{background:${C.redLo}}50%{background:rgba(239,68,68,.2)}}
  .slide-in{animation:slide-in .3s ease both}
  .blink{animation:blink 1s step-end infinite}
  input,select,textarea{background:#0a0d12;border:1px solid ${C.border};color:${C.white};
    font-family:'JetBrains Mono',monospace;font-size:12px;padding:8px 12px;
    border-radius:4px;outline:none;transition:border-color .2s;resize:vertical}
  input:focus,select:focus,textarea:focus{border-color:${C.amber}}
  input::placeholder,textarea::placeholder{color:${C.muted}}
  button{cursor:pointer;font-family:'JetBrains Mono',monospace;font-size:11px;border:none;border-radius:4px;transition:all .15s}
  button:active{transform:scale(.97)}
  button:disabled{opacity:.5;cursor:not-allowed}
  .issue-card{border:1px solid ${C.border};border-radius:6px;background:${C.panel};cursor:pointer;transition:border-color .2s,background .2s;overflow:hidden}
  .issue-card:hover{border-color:${C.borderHi};background:#111820}
  .issue-card.alert{animation:flash-r 2s ease infinite;border-color:rgba(239,68,68,.5)!important}
  .issue-card.expanded{border-color:${C.amberDim}!important}
  .tab{cursor:pointer;padding:8px 16px;font-size:11px;font-weight:700;letter-spacing:.05em;border-bottom:2px solid transparent;transition:all .2s;color:${C.muted}}
  .tab.active{color:${C.amber};border-bottom-color:${C.amber}}
  .tab:hover:not(.active){color:${C.text}}
`;

// ─── Components ───────────────────────────────────────────────────────────────
function Spinner({color=C.amber,size=14}){
  return <span style={{display:"inline-block",width:size,height:size,border:`2px solid ${C.border}`,borderTopColor:color,borderRadius:"50%",animation:"spin .8s linear infinite"}}/>;
}

function CountdownRing({s,total}){
  const r=16,circ=2*Math.PI*r,dash=circ*(s/total);
  return(
    <svg width={40} height={40} style={{transform:"rotate(-90deg)"}}>
      <circle cx={20} cy={20} r={r} fill="none" stroke={C.border} strokeWidth={3}/>
      <circle cx={20} cy={20} r={r} fill="none" stroke={C.amber} strokeWidth={3}
        strokeDasharray={`${dash} ${circ}`} strokeLinecap="round"
        style={{transition:"stroke-dasharray 1s linear"}}/>
      <text x={20} y={20} textAnchor="middle" dominantBaseline="central"
        fill={C.text} fontSize={8} fontFamily="JetBrains Mono"
        style={{transform:"rotate(90deg)",transformOrigin:"20px 20px"}}>{s}s</text>
    </svg>
  );
}

function IssueCard({issue,idx}){
  const [open,setOpen]=useState(false);
  const sev=getSev(issue.severity);
  const hasAlert=issue.triggeredBy?.length>0;
  return(
    <div className={`issue-card slide-in${hasAlert?" alert":""}${open?" expanded":""}`}
      style={{animationDelay:`${idx*35}ms`,marginBottom:8}} onClick={()=>setOpen(o=>!o)}>
      {hasAlert&&<div style={{height:2,background:`linear-gradient(90deg,${C.red},transparent)`}}/>}
      <div style={{display:"flex",alignItems:"center",gap:10,padding:"12px 16px"}}>
        <div style={{width:8,height:8,borderRadius:"50%",background:sev.color,boxShadow:`0 0 6px ${sev.color}`,flexShrink:0,animation:hasAlert?"pulse-r 1.5s infinite":"none"}}/>
        <div style={{flex:1,minWidth:0}}>
          <div style={{display:"flex",alignItems:"center",gap:8,flexWrap:"wrap"}}>
            <span style={{fontSize:9,fontWeight:700,color:C.amberDim}}>{issue.kind}</span>
            <span style={{color:C.white,fontSize:12,fontWeight:500}}>{issue.name}</span>
          </div>
          <div style={{fontSize:9,color:C.textDim,marginTop:2}}>ns: <span style={{color:C.amber}}>{issue.namespace}</span></div>
        </div>
        <div style={{display:"flex",gap:4,flexWrap:"wrap",justifyContent:"flex-end",flexShrink:0}}>
          {issue.triggeredBy?.map(code=>{
            const e=WATCHED.find(w=>w.code===code);
            return <span key={code} style={{fontSize:9,fontWeight:700,padding:"2px 6px",borderRadius:3,background:e?.color+"20",color:e?.color,border:`1px solid ${e?.color}40`}}>{code==="TOT"?"TIMEOUT":code==="CON"?"CONN":`HTTP ${code}`}</span>;
          })}
          <span style={{fontSize:9,fontWeight:700,padding:"2px 6px",borderRadius:3,background:sev.color+"20",color:sev.color}}>{sev.label}</span>
          <span style={{color:C.muted,fontSize:16,transform:open?"rotate(90deg)":"none",transition:"transform .2s"}}>›</span>
        </div>
      </div>
      <div style={{padding:"0 16px 12px 34px",fontSize:11,color:C.textDim,fontStyle:"italic"}}>{issue.error}</div>
      {open&&(
        <div style={{borderTop:`1px solid ${C.border}`,background:C.panel2,padding:"14px 16px"}}>
          <div style={{fontSize:9,color:C.amber,letterSpacing:"0.1em",marginBottom:8}}>◆ SRE ANALYSIS</div>
          <pre style={{fontSize:11,color:C.text,lineHeight:1.8,borderLeft:`2px solid ${C.amberDim}`,paddingLeft:12,whiteSpace:"pre-wrap",fontFamily:"inherit"}}>{issue.details}</pre>
        </div>
      )}
    </div>
  );
}

// ─── Tab: Dashboard ───────────────────────────────────────────────────────────
function TabDashboard({apiUrl,useMock}){
  const [watching,setWatching]=useState(false);
  const [loading,setLoading]=useState(false);
  const [issues,setIssues]=useState([]);
  const [error,setError]=useState("");
  const [lastScan,setLastScan]=useState(null);
  const [countdown,setCountdown]=useState(0);
  const [scanCount,setScanCount]=useState(0);
  const [pollInterval,setPollInterval]=useState(30);
  const [showAlert,setShowAlert]=useState(false);
  const [filter,setFilter]=useState("all");
  const [search,setSearch]=useState("");
  const [log,setLog]=useState([]);
  const timerRef=useRef(),countRef=useRef(),prevAlerts=useRef(0);

  function addLog(msg,type="info"){setLog(l=>[{ts:new Date().toLocaleTimeString(),msg,type},...l].slice(0,20));}

  const analyze=useCallback(async(silent=false)=>{
    if(!silent)setLoading(true);
    setError("");
    try{
      let raw=[];
      if(useMock){
        await new Promise(r=>setTimeout(r,silent?300:1100));
        raw=MOCK;
      }else{
        const res=await fetch(`${apiUrl}/v1/analyze`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({namespace:"",explain:true,filters:[]}),signal:AbortSignal.timeout(15000)});
        if(!res.ok)throw new Error(`HTTP ${res.status}`);
        const data=await res.json();
        raw=(data?.results||[]).map((r,i)=>({id:String(i),kind:r.kind||"Unknown",name:r.name||"unknown",namespace:r.namespace||"default",error:Array.isArray(r.error)?r.error.join("; "):(r.error||""),details:r.details||"",severity:r.severity||"info"}));
      }
      const tagged=tagIssues(raw);
      setIssues(tagged);setLastScan(new Date());setScanCount(c=>c+1);
      const ac=tagged.filter(i=>i.triggeredBy?.length>0).length;
      if(ac>0&&ac!==prevAlerts.current){setShowAlert(true);addLog(`⚠ ${ac} issue(s) com erros HTTP`,"error");}
      else if(ac===0&&tagged.length>0)addLog(`✓ ${tagged.length} issue(s) — sem erros críticos`,"ok");
      else addLog(`✓ Cluster saudável`,"ok");
      prevAlerts.current=ac;
    }catch(e){const m=e.name==="TimeoutError"?"Timeout":e.message;setError(m);addLog(`✗ ${m}`,"error");}
    finally{setLoading(false);}
  },[apiUrl,useMock]);

  function start(){setWatching(true);setCountdown(pollInterval);addLog(`▶ Watching · ${pollInterval}s · all namespaces`,"info");analyze(false);}
  function stop(){setWatching(false);clearInterval(timerRef.current);clearInterval(countRef.current);addLog("■ Stopped","info");}

  useEffect(()=>{
    if(!watching)return;
    timerRef.current=setInterval(()=>{setCountdown(pollInterval);analyze(true);},pollInterval*1000);
    countRef.current=setInterval(()=>setCountdown(c=>Math.max(0,c-1)),1000);
    return()=>{clearInterval(timerRef.current);clearInterval(countRef.current);};
  },[watching,pollInterval,analyze]);

  useEffect(()=>{analyze(false);},[]);

  const errCounts=Object.fromEntries(WATCHED.map(e=>[e.code,issues.filter(i=>i.triggeredBy?.includes(e.code)).length]));
  const alertCount=issues.filter(i=>i.triggeredBy?.length>0).length;
  const stats=issues.reduce((a,i)=>{if(a[i.severity]!==undefined)a[i.severity]++;return a;},{critical:0,error:0,warning:0,info:0});
  const filtered=issues.filter(i=>{
    const mf=filter==="all"?true:filter==="alerts"?i.triggeredBy?.length>0:i.severity===filter;
    const q=search.toLowerCase();
    const ms=!q||[i.name,i.namespace,i.kind,i.error].some(v=>v.toLowerCase().includes(q));
    return mf&&ms;
  });

  return(
    <div>
      {/* Controls */}
      <div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:6,padding:"14px 18px",marginBottom:14,display:"flex",flexWrap:"wrap",gap:10,alignItems:"flex-end"}}>
        <div style={{minWidth:110}}>
          <div style={{fontSize:9,color:C.textDim,marginBottom:4}}>INTERVALO</div>
          <select value={pollInterval} onChange={e=>setPollInterval(Number(e.target.value))} style={{width:"100%"}}>
            {POLL_OPTS.map(s=><option key={s} value={s}>{s}s</option>)}
          </select>
        </div>
        {!watching
          ?<button onClick={start} style={{background:C.amber,color:"#000",fontWeight:700,padding:"9px 18px",display:"flex",alignItems:"center",gap:8}}>▶ START WATCHING</button>
          :<button onClick={stop} style={{background:C.redLo,color:C.red,fontWeight:700,border:`1px solid ${C.red}40`,padding:"9px 18px"}}>■ STOP</button>
        }
        <button onClick={()=>analyze(false)} disabled={loading} style={{background:"transparent",color:C.text,border:`1px solid ${C.border}`,padding:"9px 14px",display:"flex",alignItems:"center",gap:6}}>
          {loading?<Spinner size={12}/>:"↻"} SCAN NOW
        </button>
      </div>

      {/* Alert banner */}
      {showAlert&&alertCount>0&&(
        <div style={{background:C.redLo,border:`1px solid ${C.red}40`,borderRadius:6,padding:"12px 16px",marginBottom:12,display:"flex",alignItems:"center",gap:12,animation:"flash-r 2s ease infinite"}}>
          <div style={{width:10,height:10,borderRadius:"50%",background:C.red,animation:"pulse-r 1.5s infinite",flexShrink:0}}/>
          <div style={{flex:1,fontSize:11,fontWeight:700,color:C.red}}>ERROS DETECTADOS — {alertCount} ISSUE(S) ATIVOS</div>
          <button onClick={()=>setShowAlert(false)} style={{background:"transparent",color:C.muted,padding:"4px 8px",fontSize:14}}>✕</button>
        </div>
      )}

      {error&&<div style={{background:C.redLo,border:`1px solid ${C.red}40`,borderRadius:6,padding:"10px 14px",fontSize:11,color:C.red,marginBottom:12}}>✗ {error}</div>}

      {/* Watcher badges */}
      <div style={{display:"flex",flexWrap:"wrap",gap:6,marginBottom:14,alignItems:"center"}}>
        <span style={{fontSize:9,color:C.textDim,letterSpacing:"0.1em",marginRight:2}}>WATCHING:</span>
        {WATCHED.map(e=>{const hit=errCounts[e.code]||0;return(
          <div key={e.code} style={{display:"flex",alignItems:"center",gap:5,padding:"4px 10px",background:hit>0?e.color+"18":C.panel,border:`1px solid ${hit>0?e.color+"50":C.border}`,borderRadius:4,transition:"all .3s"}}>
            <div style={{width:5,height:5,borderRadius:"50%",background:hit>0?e.color:C.muted,boxShadow:hit>0?`0 0 6px ${e.color}`:"none"}}/>
            <span style={{fontSize:9,fontWeight:700,color:hit>0?e.color:C.muted}}>{e.code==="TOT"?"TIMEOUT":e.code==="CON"?"CONN":e.code}</span>
            {hit>0&&<span style={{fontSize:9,fontWeight:700,color:C.bg,background:e.color,borderRadius:3,padding:"0 4px"}}>{hit}</span>}
          </div>
        );})}
      </div>

      {/* Stats */}
      {issues.length>0&&(
        <div style={{display:"flex",gap:8,flexWrap:"wrap",marginBottom:14}} className="slide-in">
          {[{l:"TOTAL",v:issues.length,c:C.white},{l:"ALERTS",v:alertCount,c:C.red},{l:"CRITICAL",v:stats.critical,c:C.red},{l:"ERRORS",v:stats.error,c:C.red},{l:"WARNS",v:stats.warning,c:C.yellow},{l:"SCANS",v:scanCount,c:C.blue}].map(s=>(
            <div key={s.l} style={{flex:1,minWidth:70,background:C.panel,border:`1px solid ${C.border}`,borderRadius:6,padding:"10px 12px"}}>
              <div style={{fontSize:8,color:C.textDim,marginBottom:4}}>{s.l}</div>
              <div style={{fontSize:22,fontWeight:700,color:s.c,lineHeight:1}}>{s.v}</div>
            </div>
          ))}
          {watching&&<div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:6,padding:"6px 10px",display:"flex",flexDirection:"column",alignItems:"center",gap:2}}><CountdownRing s={countdown} total={pollInterval}/><div style={{fontSize:8,color:C.textDim}}>PRÓXIMO</div></div>}
        </div>
      )}

      {/* Filters */}
      {issues.length>0&&(
        <div style={{display:"flex",flexWrap:"wrap",gap:8,alignItems:"center",marginBottom:12}}>
          <input value={search} onChange={e=>setSearch(e.target.value)} placeholder="buscar resource, namespace…" style={{flex:1,minWidth:140}}/>
          {["all","alerts","critical","error","warning","info"].map(f=>{
            const active=filter===f,col=f==="alerts"?C.red:SEV[f]?.color||C.amber;
            return<button key={f} onClick={()=>setFilter(f)} style={{padding:"5px 10px",background:active?col+"20":"transparent",border:`1px solid ${active?col:C.border}`,color:active?col:C.muted,fontSize:9,fontWeight:700}}>
              {f.toUpperCase()}{f==="alerts"&&alertCount>0&&<span style={{marginLeft:4,background:C.red,color:"#000",borderRadius:3,padding:"0 4px",fontSize:8}}>{alertCount}</span>}
            </button>;
          })}
        </div>
      )}

      {/* Issues */}
      {loading&&<div style={{display:"flex",flexDirection:"column",alignItems:"center",gap:14,padding:"50px 0",color:C.textDim,fontSize:12}}><Spinner size={20}/><span>analisando todas as namespaces…</span></div>}
      {!loading&&issues.length===0&&<div style={{textAlign:"center",padding:"50px 0",color:C.textDim,fontSize:12}}><span style={{color:C.green}}>✓ nenhum issue detectado</span></div>}
      {!loading&&filtered.map((issue,i)=><IssueCard key={issue.id} issue={issue} idx={i}/>)}

      {/* Log */}
      {log.length>0&&(
        <div style={{marginTop:24,background:C.panel,border:`1px solid ${C.border}`,borderRadius:6,padding:"14px 16px"}}>
          <div style={{fontSize:9,color:C.textDim,letterSpacing:"0.1em",marginBottom:10}}>ACTIVITY LOG</div>
          <div style={{display:"flex",flexDirection:"column",gap:5,maxHeight:130,overflowY:"auto"}}>
            {log.map((e,i)=><div key={i} style={{display:"flex",gap:10,fontSize:10}}><span style={{color:C.muted,flexShrink:0}}>{e.ts}</span><span style={{color:e.type==="error"?C.red:e.type==="ok"?C.green:C.text}}>{e.msg}</span></div>)}
          </div>
        </div>
      )}

      <div style={{marginTop:20,fontSize:9,color:C.muted,textAlign:"right"}}>{lastScan?`último scan: ${lastScan.toLocaleTimeString()}`:"aguardando…"}</div>
    </div>
  );
}

// ─── Tab: Providers ───────────────────────────────────────────────────────────
function TabProviders({selected,setSelected,model,setModel,apiKey,setApiKey,baseUrl,setBaseUrl,onSave}){
  const prov=PROVIDERS.find(p=>p.id===selected)||PROVIDERS[0];
  return(
    <div style={{display:"grid",gap:16}}>
      {/* Provider cards */}
      <div style={{display:"grid",gridTemplateColumns:"repeat(auto-fit,minmax(200px,1fr))",gap:10}}>
        {PROVIDERS.map(p=>{
          const active=selected===p.id;
          return(
            <div key={p.id} onClick={()=>{setSelected(p.id);setModel(p.models[0]);}}
              style={{background:active?p.color+"15":C.panel,border:`1px solid ${active?p.color+"60":C.border}`,borderRadius:8,padding:"16px",cursor:"pointer",transition:"all .2s"}}>
              <div style={{display:"flex",alignItems:"center",gap:10,marginBottom:10}}>
                <span style={{fontSize:22}}>{p.icon}</span>
                <div>
                  <div style={{fontSize:12,fontWeight:700,color:active?p.color:C.white}}>{p.name}</div>
                  <div style={{fontSize:9,color:C.textDim}}>{p.vendor}</div>
                </div>
                {p.local&&<span style={{marginLeft:"auto",fontSize:8,fontWeight:700,padding:"2px 6px",borderRadius:3,background:C.greenLo,color:C.green,border:`1px solid ${C.green}30`}}>LOCAL</span>}
                {active&&<span style={{marginLeft:p.local?"4px":"auto",fontSize:8,fontWeight:700,padding:"2px 6px",borderRadius:3,background:p.color+"20",color:p.color}}>ACTIVE</span>}
              </div>
              <div style={{fontSize:10,color:C.textDim,lineHeight:1.6}}>{p.desc}</div>
            </div>
          );
        })}
      </div>

      {/* Config panel */}
      <div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:8,padding:"20px"}}>
        <div style={{fontSize:10,fontWeight:700,color:prov.color,letterSpacing:"0.1em",marginBottom:16,display:"flex",alignItems:"center",gap:8}}>
          <span>{prov.icon}</span> CONFIGURAR {prov.name.toUpperCase()}
        </div>

        <div style={{display:"grid",gap:12}}>
          {/* Model */}
          <div>
            <div style={{fontSize:9,color:C.textDim,marginBottom:4}}>MODELO</div>
            <select value={model} onChange={e=>setModel(e.target.value)} style={{width:"100%"}}>
              {prov.models.map(m=><option key={m} value={m}>{m}</option>)}
            </select>
          </div>

          {/* API Key */}
          {prov.requiresKey&&(
            <div>
              <div style={{fontSize:9,color:C.textDim,marginBottom:4}}>API KEY</div>
              <input type="password" value={apiKey} onChange={e=>setApiKey(e.target.value)} placeholder={prov.placeholder} style={{width:"100%"}}/>
            </div>
          )}

          {/* Base URL */}
          {prov.id==="ollama"&&(
            <div>
              <div style={{fontSize:9,color:C.textDim,marginBottom:4}}>OLLAMA URL</div>
              <input value={baseUrl} onChange={e=>setBaseUrl(e.target.value)} placeholder={prov.placeholder} style={{width:"100%"}}/>
            </div>
          )}

          {prov.id==="bedrock"&&(
            <div style={{background:C.amberLo,border:`1px solid ${C.amberDim}`,borderRadius:4,padding:"10px 12px",fontSize:11,color:C.text}}>
              💡 Bedrock usa credenciais IAM do ambiente (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY ou IAM Role do pod).
            </div>
          )}

          <button onClick={onSave} style={{background:prov.color,color:"#000",fontWeight:700,padding:"10px 20px",fontSize:11,letterSpacing:"0.05em"}}>
            ✓ SALVAR E APLICAR
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Tab: SRE Prompt ──────────────────────────────────────────────────────────
function TabPrompt({prompt,setPrompt,onReset}){
  const [copied,setCopied]=useState(false);
  function copy(){navigator.clipboard?.writeText(prompt);setCopied(true);setTimeout(()=>setCopied(false),2000);}
  return(
    <div style={{display:"grid",gap:16}}>
      <div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:8,padding:"20px"}}>
        <div style={{display:"flex",alignItems:"center",justifyContent:"space-between",marginBottom:14}}>
          <div style={{fontSize:10,fontWeight:700,color:C.purple,letterSpacing:"0.1em"}}>🧠 SRE SYSTEM PROMPT</div>
          <div style={{display:"flex",gap:8}}>
            <button onClick={copy} style={{background:C.panel2,border:`1px solid ${C.border}`,color:copied?C.green:C.text,padding:"6px 12px"}}>
              {copied?"✓ Copiado":"⎘ Copiar"}
            </button>
            <button onClick={onReset} style={{background:C.panel2,border:`1px solid ${C.border}`,color:C.muted,padding:"6px 12px"}}>
              ↺ Reset
            </button>
          </div>
        </div>
        <textarea value={prompt} onChange={e=>setPrompt(e.target.value)}
          rows={24} style={{width:"100%",fontSize:11,lineHeight:1.7,fontFamily:"'JetBrains Mono',monospace"}}/>
      </div>

      <div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:8,padding:"20px"}}>
        <div style={{fontSize:10,fontWeight:700,color:C.amber,letterSpacing:"0.1em",marginBottom:12}}>⚡ COMO USAR NO CLAUDE DESKTOP</div>
        <div style={{background:C.panel2,border:`1px solid ${C.border}`,borderRadius:4,padding:"14px",fontSize:11,color:C.text,lineHeight:1.8}}>
          <div style={{color:C.amber,marginBottom:8}}>~/.config/claude/claude_desktop_config.json</div>
          <pre style={{color:C.green,fontSize:10,lineHeight:1.7,whiteSpace:"pre-wrap"}}>{`{
  "mcpServers": {
    "devopsgpt": {
      "url": "http://localhost:8089/mcp"
    }
  }
}`}</pre>
          <div style={{marginTop:12,color:C.textDim}}>
            O MCP server envia o SRE prompt automaticamente via <span style={{color:C.amber}}>initialize.instructions</span>.<br/>
            O Claude Desktop vai agir como SRE specialist em toda sessão.
          </div>
        </div>
      </div>

      <div style={{background:C.purpleLo,border:`1px solid ${C.purple}30`,borderRadius:8,padding:"16px",fontSize:11,color:C.text,lineHeight:1.7}}>
        <div style={{color:C.purple,fontWeight:700,marginBottom:8}}>💬 Exemplos de perguntas pro Claude Desktop</div>
        {["Quais pods estão em CrashLoop na produção?","Por que o auth-gateway está retornando 401?","Resume o estado de saúde do cluster","Qual namespace tem mais erros hoje?","Sugira um rollback para o payment-api"].map(q=>(
          <div key={q} style={{padding:"4px 0",color:C.textDim}}>› <span style={{color:C.text}}>{q}</span></div>
        ))}
      </div>
    </div>
  );
}

// ─── Main App ─────────────────────────────────────────────────────────────────
export default function App(){
  const [tab,setTab]=useState("dashboard");
  const [apiUrl,setApiUrl]=useState("http://localhost:8080");
  const [useMock,setUseMock]=useState(true);
  const [watching,setWatching]=useState(false);

  // Provider state
  const [provider,setProvider]=useState("claude");
  const [model,setModel]=useState("claude-sonnet-4-20250514");
  const [apiKey,setApiKey]=useState("");
  const [baseUrl,setBaseUrl]=useState("http://localhost:11434");
  const [savedProvider,setSavedProvider]=useState(null);

  // Prompt state
  const [prompt,setPrompt]=useState(SRE_PROMPT);

  function saveProvider(){
    setSavedProvider({provider,model,apiKey,baseUrl});
    setTab("dashboard");
  }

  const prov=PROVIDERS.find(p=>p.id===provider)||PROVIDERS[0];

  return(
    <>
      <style>{STYLES}</style>
      <div style={{maxWidth:920,margin:"0 auto",padding:"28px 16px 60px",position:"relative",zIndex:1}}>

        {/* Header */}
        <div style={{marginBottom:20}}>
          <div style={{display:"flex",alignItems:"center",gap:10,marginBottom:4}}>
            <div style={{width:10,height:10,borderRadius:"50%",background:watching?C.green:C.muted,animation:watching?"pulse-g 2s infinite":"none"}}/>
            <span style={{fontSize:13,fontWeight:700,color:C.amber,letterSpacing:"0.15em",fontFamily:"Syne,sans-serif"}}>DEVOPSGPT</span>
            <span className="blink" style={{color:C.amber}}>_</span>
            {savedProvider&&(
              <span style={{fontSize:9,padding:"2px 8px",borderRadius:3,background:prov.color+"20",color:prov.color,border:`1px solid ${prov.color}40`}}>
                {prov.icon} {prov.name} · {model.split(/[-/.]/)[0]}
              </span>
            )}
          </div>
          <div style={{fontSize:10,color:C.textDim}}>kubernetes ai operator · all-namespace monitor · sre specialist</div>
        </div>

        {/* Global config */}
        <div style={{background:C.panel,border:`1px solid ${C.border}`,borderRadius:6,padding:"12px 16px",marginBottom:16,display:"flex",flexWrap:"wrap",gap:10,alignItems:"center"}}>
          <div style={{flex:2,minWidth:140}}>
            <div style={{fontSize:9,color:C.textDim,marginBottom:4}}>API URL</div>
            <input value={apiUrl} onChange={e=>setApiUrl(e.target.value)} placeholder="http://localhost:8080" style={{width:"100%"}} disabled={useMock}/>
          </div>
          <div style={{display:"flex",alignItems:"center",gap:6}}>
            <input type="checkbox" id="mock" checked={useMock} onChange={e=>setUseMock(e.target.checked)} style={{width:14,height:14,accentColor:C.amber}}/>
            <label htmlFor="mock" style={{fontSize:10,color:C.textDim,cursor:"pointer"}}>demo mode</label>
          </div>
        </div>

        {/* Tabs */}
        <div style={{display:"flex",borderBottom:`1px solid ${C.border}`,marginBottom:20}}>
          {[{id:"dashboard",label:"⬡ Dashboard"},{id:"providers",label:"⚙ Providers"},{id:"prompt",label:"🧠 SRE Prompt"}].map(t=>(
            <div key={t.id} className={`tab${tab===t.id?" active":""}`} onClick={()=>setTab(t.id)}>{t.label}</div>
          ))}
        </div>

        {/* Tab content */}
        {tab==="dashboard"&&<TabDashboard apiUrl={apiUrl} useMock={useMock}/>}
        {tab==="providers"&&<TabProviders selected={provider} setSelected={setProvider} model={model} setModel={setModel} apiKey={apiKey} setApiKey={setApiKey} baseUrl={baseUrl} setBaseUrl={setBaseUrl} onSave={saveProvider}/>}
        {tab==="prompt"&&<TabPrompt prompt={prompt} setPrompt={setPrompt} onReset={()=>setPrompt(SRE_PROMPT)}/>}

        {/* Footer */}
        <div style={{marginTop:32,borderTop:`1px solid ${C.border}`,paddingTop:12,display:"flex",justifyContent:"space-between",fontSize:9,color:C.muted,flexWrap:"wrap",gap:6}}>
          <span>devopsgpt · all-namespace · {savedProvider?`${prov.name} / ${model}`:"configure provider →"}</span>
          <span style={{color:C.amberDim}}>REST :8080 · MCP :8089</span>
        </div>
      </div>
    </>
  );
}
