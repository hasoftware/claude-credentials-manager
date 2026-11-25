(() => {
  "use strict";
  const el = id => document.getElementById(id);
  const form = el("monitor-form");
  const detail = el("monitor-settings");
  const feedback = el("monitor-feedback");
  const clock = new Intl.DateTimeFormat("vi-VN",{dateStyle:"short",timeStyle:"medium",timeZone:"Asia/Ho_Chi_Minh"});
  const date = v => v && !v.startsWith("0001") ? clock.format(new Date(v)) : "—";
  const node = (tag,text,cls) => {
    const n=document.createElement(tag);
    if (text!==undefined) n.textContent=text;
    if (cls) n.className=cls;
    return n;
  };
  function section(title,caption) {
    const p=node("section",undefined,"report-panel");
    p.append(node("h2",title));
    if(caption) p.append(node("p",caption,"system-caption"));
    return p;
  }
  function list(parent, rows, empty) {
    if(!rows.length) {parent.append(node("p",empty,"muted"));return;}
    const l=node("ul",undefined,"system-list");
    for(const [title,value,problem] of rows) {
      const r=node("li");r.append(node("strong",title),node("span",value,problem?"problem":undefined));l.append(r);
    }
    parent.append(l);
  }
  function metric(box,label,value,note,percent) {
    const c=node("article");
    c.append(node("span",label),node("strong",value),node("small",note));
    if(Number.isFinite(percent)) {const m=node("progress");m.max=100;m.value=percent;m.setAttribute("aria-label",label);c.append(m);}
    box.append(c);
  }
  const size=n=>(n/1073741824).toLocaleString("en",{maximumFractionDigits:2})+" GiB";
  function render(r) {
    const health=el("health");
    const connections=el("connections");
    const incidents=el("incidents");
    const time=el("monitor-time");
    if(r.waiting){for(const group of [health,connections,incidents]) group.replaceChildren();time.textContent="Waiting for ccm-monitor to publish its first sample.";return;}
    const stale=Date.now()-Date.parse(r.At)>90000;
    time.classList.toggle("stale",stale);
    time.textContent=(stale?"Monitor data is stale · ":"Last sample · ")+date(r.At)+" ICT · "+r.Host.Host;
    const resources=section("System resources",r.Host.Host+" · Linux environment · uptime "+Math.floor(r.Host.Uptime/3600)+"h");
    const meters=node("div",undefined,"system-metrics");
    metric(meters,"CPU",r.Host.CPU==null?"—":r.Host.CPU.toFixed(1)+"%",r.Host.Cores+" visible cores",r.Host.CPU);
    for(const [label,usage] of [["RAM",r.Host.RAM],["Swap",r.Host.Swap],["Data disk",r.Host.Disk]]) {
      const pct=usage?.Total?usage.Used/usage.Total*100:null;
      metric(meters,label,usage?size(usage.Used):"—",usage?"of "+size(usage.Total):"Unavailable",pct);
    }
    resources.append(meters);
    const services=section("Services","systemd state, listener status and restart count.");
    list(services,(r.Services||[]).map(s=>[s.Name,s.State+" · "+s.Detail+" · "+s.Restarts+" restarts"+(s.Since?" · "+s.Since:""),s.State!=="active"]),"No service data.");
    const tunnel=section("Cloudflare Tunnel","Each cloudflared process is one connector. Each connector can maintain several edge connections.");
    const inventory=r.Cloud.State==="reported";
    const cloudMeters=node("div",undefined,"system-metrics");
    metric(cloudMeters,"Local connector",r.Tunnel.Ready?"1":"—",r.Tunnel.Ready?"Ready":"Readiness unconfirmed");
    metric(cloudMeters,"Local edge links",r.Tunnel.Edges??"—","From cloudflared /ready");
    metric(cloudMeters,"Tunnel-wide connectors",inventory?(r.Cloud.Connectors||[]).length:"—",inventory?"Cloudflare · "+date(r.Cloud.At):r.Cloud.State==="not configured"?"Configure read access in Settings":"API unavailable");
    tunnel.append(cloudMeters);
    if(r.Tunnel.ID) tunnel.append(node("p","Local connector ID · "+r.Tunnel.ID,"system-caption"));
    if(inventory) list(tunnel,(r.Cloud.Connectors||[]).map(c=>[c.ID,(c.Known?"Recognized":"Unrecognized")+" · "+(c.Origins||[]).join(", ")+" · "+c.Edges+" edge links · "+c.Version,!c.Known]),"No active connector reported by Cloudflare.");
    const clients=section("Gateway clients","Established TCP connections sampled every 15 seconds. One IP can represent several clients behind a proxy or NAT. Brief connections can fall between samples.");
    const peers=r.Peers||[];
    const ipCount=new Set(peers.map(p=>p.IP)).size;
    clients.append(node("p",r.PeersAvailable?ipCount+" distinct source IPs · "+peers.reduce((sum,p)=>sum+p.Sockets,0)+" open connections":"Socket inventory unavailable."));
    clients.append(node("p","Sessions with recorded usage in the last 15 minutes · "+(r.RecentSessions??"—"),"system-caption"));
    if(peers.length) list(clients,peers.map(p=>[p.IP,p.Gateway+" · "+p.Sockets+" open"+(p.Unexpected?" · outside trusted networks":""),p.Unexpected]),"");
    clients.append(node("h3","Recently seen"));
    list(clients,(r.Recent||[]).map(p=>[p.IP,p.Gateway+" · "+(r.PeersAvailable?p.Sockets+" open":"current count unavailable")+" · last seen "+date(p.LastSeen)+(p.Unexpected?" · outside trusted networks":""),p.Unexpected]),"No sampled peers in the past 24 hours.");
    const accounts=section("Provider access","Read-only provider checks every five minutes. Last check · "+date(r.AccountsAt)+" ICT");
    if(!r.AccountsAvailable) accounts.append(node("p","Waiting for a successful account inventory.", "muted"));
    list(accounts,(r.Accounts||[]).map(a=>[a.Name,a.Provider+" · "+a.State+" · "+date(a.At),a.State!=="accepted"]),"No provider check results.");
    const history=section("Incident history","Service and tunnel readiness alerts trigger after three consecutive failures. Confirmed provider 401s and unrecognized connector IDs are reported on detection.");
    list(history,[...(r.Changes||[])].reverse().map(c=>[date(c.At),c.Message]),"No incident transitions recorded.");
    history.append(node("p","Last Telegram delivery · "+(r.LastDelivery||"No delivery recorded"),"system-caption"));
    health.replaceChildren(resources,services,accounts);
    connections.replaceChildren(tunnel,clients);
    incidents.replaceChildren(history);
  }
  async function api(url,body) {
    const opts={cache:"no-store"};
    if(body!==undefined){opts.method="POST";opts.headers={"Content-Type":"application/json"};opts.body=JSON.stringify(body);}
    const res=await fetch(url,opts);
    if(res.status===401){location.reload();throw new Error("Sign-in required");}
    const result=await res.json();if(!res.ok) throw new Error(result.error||"Request failed");return result;
  }
  let busy=false;
  async function refresh(){
    if(busy||document.hidden)return;busy=true;
    try{render(await api("/api/system"));}catch{el("monitor-time").textContent="Status unavailable. Retrying…";el("monitor-time").classList.add("stale");}finally{busy=false;}
  }
  const field=k=>form.elements.namedItem(k);
  let loaded=false;
  function showConfig(s){
    field("Telegram").checked=s.Telegram;
    for(const k of ["ChatID","AccountID","TunnelID"])field(k).value=s[k]||"";
    for(const k of ["TrustedConnectors","TrustedNetworks"])field(k).value=(s[k]||[]).join("\n");
    for(const [k,exists] of [["BotToken",s.HasBot],["CloudToken",s.HasCloud]]){field(k).value="";field(k).placeholder=exists?"Saved · leave blank to keep":"Enter token";}
    field("ClearBot").checked=false;field("ClearCloud").checked=false;
    el("monitor-test").disabled=!s.HasBot||!s.ChatID;el("monitor-save").disabled=false;loaded=true;
  }
  async function loadConfig(){try{showConfig(await api("/api/system/settings"));}catch(e){feedback.textContent=e.message;}}
  let loadingConfig=false;
  async function onSectionChange() {
    if(detail.hidden || loaded || loadingConfig) return;
    loadingConfig=true;
    try { await loadConfig(); } finally { loadingConfig=false; }
  }
  document.addEventListener("sectionchange",onSectionChange);
  onSectionChange();
  form.addEventListener("submit",async e=>{
    e.preventDefault();if(!loaded)return;
    const body={};for(const k of ["Telegram","ClearCloud","ClearBot"])body[k]=field(k).checked;
    for(const k of ["ChatID","BotToken","CloudToken","AccountID","TunnelID"])body[k]=field(k).value.trim();
    for(const k of ["TrustedConnectors","TrustedNetworks"])body[k]=field(k).value.split(/[\s,]+/).filter(Boolean);
    el("monitor-save").disabled=true;
    try{showConfig(await api("/api/system/settings",body));feedback.textContent="Settings saved. The monitor applies them on its next sample.";}catch(err){feedback.textContent=err.message;}finally{el("monitor-save").disabled=false;}
  });
  el("monitor-test").addEventListener("click",async()=>{
    el("monitor-test").disabled=true;
    try{await api("/api/system/telegram/test",{});feedback.textContent="Test message delivered.";}catch(e){feedback.textContent=e.message;}finally{el("monitor-test").disabled=false;}
  });
  document.addEventListener("visibilitychange",refresh);refresh();setInterval(refresh,15000);
})();
