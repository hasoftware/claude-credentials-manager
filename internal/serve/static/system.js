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

  function badge(label, tone = "neutral") {
    const chip = node("span", label, "monitor-badge");
    chip.dataset.tone = tone;
    return chip;
  }

  const stateTones = new Map([
    ["active", "good"], ["accepted", "good"],
    ["activating", "warn"], ["deactivating", "warn"], ["maintenance", "warn"],
    ["reloading", "warn"], ["refreshing", "warn"], ["rate limited", "warn"],
    ["failed", "bad"], ["inactive", "bad"], ["unreachable", "bad"],
    ["authorization rejected", "bad"], ["access token expired", "bad"]
  ]);

  function stateBadge(state) {
    if (!state) return badge("Unknown");
    return badge(state[0].toUpperCase() + state.slice(1), stateTones.get(state));
  }

  function list(parent, rows, empty) {
    if (!rows.length) {
      parent.append(node("p", empty, "muted"));
      return;
    }
    const items = node("ul", undefined, "system-list");
    for (const [title, chip, description] of rows) {
      const item = node("li");
      const info = node("div", undefined, "status-detail");
      info.append(chip);
      if (description) info.append(node("span", description));
      item.append(node("strong", title), info);
      items.append(item);
    }
    parent.append(items);
  }

  function metric(box, label, value, note, percent) {
    const card = node("article");
    card.append(node("span", label), node("strong", value));
    if (typeof note === "string") card.append(node("small", note));
    else card.append(note);
    if (Number.isFinite(percent)) {
      const meter = node("progress");
      meter.max = 100;
      meter.value = percent;
      meter.setAttribute("aria-label", label);
      card.append(meter);
    }
    box.append(card);
  }

  function readiness(tunnel) {
    if (tunnel.Ready) return badge("Ready", "good");
    return tunnel.Edges == null ? badge("Unconfirmed") : badge("Not ready", "bad");
  }

  const routeIcons = {
    origin: "M3 4h18v7H3zM3 13h18v7H3zM6 7.5h1m-1 9h1",
    connector: "M3 20v-8a9 9 0 0 1 18 0v8m-14 0v-8a5 5 0 0 1 10 0v8M10 20v-7h4v7",
    edge: "M6 18h12a4 4 0 0 0 0-8 6 6 0 0 0-11.7-1.2A4.6 4.6 0 0 0 6 18Z",
    internet: "M21 12a9 9 0 1 1-18 0 9 9 0 1 1 18 0ZM3 12h18M12 3c5 5 5 13 0 18-5-5-5-13 0-18Z"
  };

  function routeIcon(kind) {
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS(ns, "path");
    path.setAttribute("d", routeIcons[kind]);
    svg.append(path);
    return svg;
  }

  function originLabel(origin) {
    if (origin.Kind === "loopback" && origin.Address) {
      const [scheme, authority] = origin.Address.split("://");
      if (authority) return ["Localhost · " + scheme.toUpperCase(), authority];
    }
    const titles = {socket: "Unix socket", network: "Network service", "built-in": "Built-in response"};
    return ["Origin", titles[origin.Kind] || "Origin unavailable"];
  }

  function routeStage(kind, label, note, className) {
    const card = node("div", undefined, "route-node " + className);
    const text = node("div");
    text.append(node("strong", label), node("small", note));
    card.append(routeIcon(kind), text);
    return card;
  }

  function routeMap(report) {
    const panel = section("Tunnel routes", "cloudflared opens an outbound tunnel to Cloudflare. Requests from the Internet travel back through it to the origin for each hostname.");
    if (!report.Routes?.length) {
      panel.append(badge(Array.isArray(report.Routes) ? "No hostname routes configured" : "Route data unavailable"));
      return panel;
    }
    const map = node("div", undefined, "route-map");
    const hosts = node("ul", undefined, "route-hosts");
    hosts.setAttribute("aria-label", "Origins and their public hostnames");
    for (const route of report.Routes) {
      const row = node("li");
      const item = node("div", undefined, "route-node route-origin");
      const text = node("div");
      for (const origin of route.Origins?.length ? route.Origins : [{Kind: "unknown"}]) {
        const [kind, address] = originLabel(origin);
        text.append(node("small", kind), node("strong", address, "origin-address"));
      }
      const hostname = node(route.Web ? "a" : "span", route.Hostname, "route-hostname");
      if (route.Web) {
        hostname.href = "https://" + route.Hostname + "/";
        hostname.target = "_blank";
        hostname.rel = "noopener noreferrer";
      }
      text.append(hostname);
      item.append(routeIcon("origin"), text);
      row.append(item);
      hosts.append(row);
    }
    const connector = routeStage("connector", "cloudflared", report.Host.Host, "route-target");
    const details = node("div", undefined, "route-readiness");
    details.append(readiness(report.Tunnel));
    connector.append(details);
    const edge = routeStage("edge", "Cloudflare edge", report.Tunnel.Edges == null ? "Edge links unconfirmed" : report.Tunnel.Edges + " edge links", "route-edge");
    const internet = routeStage("internet", "Internet", "Visitors", "route-internet");
    map.append(hosts, connector, edge, internet);
    panel.append(map);
    return panel;
  }

  function peerRow(peer, available, recent) {
    let chip;
    if (peer.Unexpected) chip = badge("Outside trusted networks", "warn");
    else if (!available) chip = badge("Current state unknown");
    else chip = peer.Sockets > 0 ? badge("Connected", "good") : badge("Previously seen");
    const detail = [peer.Gateway];
    if (available) detail.push(peer.Sockets + " open");
    if (recent) detail.push("Last seen " + date(peer.LastSeen));
    return [peer.IP, chip, detail.join(" · ")];
  }
  const size=n=>(n/1073741824).toLocaleString("en",{maximumFractionDigits:2})+" GiB";
  function render(r) {
    const health=el("health");
    const connections=el("connections");
    const incidents=el("incidents");
    const time=el("monitor-time");
    if(r.waiting){for(const group of [health,connections,incidents]) group.replaceChildren();time.replaceChildren(badge("Waiting for first sample"));return;}
    const sampledAt = Date.parse(r.At);
    const stale = !Number.isFinite(sampledAt) || Date.now() - sampledAt > 90000;
    time.replaceChildren(stale ? badge("Stale sample", "warn") : badge("Latest sample", "good"), " " + date(r.At) + " ICT · " + r.Host.Host);
    const resources=section("System resources",r.Host.Host+" · Linux environment · uptime "+Math.floor(r.Host.Uptime/3600)+"h");
    const meters=node("div",undefined,"system-metrics");
    metric(meters,"CPU",r.Host.CPU==null?"—":r.Host.CPU.toFixed(1)+"%",r.Host.Cores+" visible cores",r.Host.CPU);
    for(const [label,usage] of [["RAM",r.Host.RAM],["Swap",r.Host.Swap],["Data disk",r.Host.Disk]]) {
      const pct=usage?.Total?usage.Used/usage.Total*100:null;
      metric(meters,label,usage?size(usage.Used):"—",usage?"of "+size(usage.Total):"Unavailable",pct);
    }
    resources.append(meters);

    const services = section("Services", "systemd state, listener status and restart count.");
    list(services, (r.Services || []).map(s => [
      s.Name, stateBadge(s.State), [s.Detail, s.Restarts + " restarts", s.Since].filter(Boolean).join(" · ")
    ]), "No service data.");

    const tunnel = section("Tunnel connectors", "Each cloudflared process is one connector. A connector can maintain several edge connections.");
    const inventory = r.Cloud.State === "reported";
    const connectors = r.Cloud.Connectors || [];
    const cloudMeters = node("div", undefined, "system-metrics");
    metric(cloudMeters, "Local connector", r.Tunnel.Ready ? "1" : "—", readiness(r.Tunnel));
    metric(cloudMeters, "Local edge links", r.Tunnel.Edges ?? "—", "From cloudflared /ready");
    let inventoryBadge;
    if (inventory) {
      if (!connectors.length) inventoryBadge = badge("No active connectors", "bad");
      else if (connectors.some(c => !c.Known)) inventoryBadge = badge("Unrecognized connector", "warn");
      else inventoryBadge = badge("Recognized", "good");
    } else if (r.Cloud.State === "not configured") inventoryBadge = badge("Not configured");
    else inventoryBadge = badge("Inventory unavailable", "warn");
    metric(cloudMeters, "Tunnel-wide connectors", inventory ? connectors.length : "—", inventoryBadge);
    tunnel.append(cloudMeters);
    if (r.Cloud.State === "not configured") tunnel.append(node("p", "Configure tunnel inventory access in Settings.", "system-caption"));
    if (r.Tunnel.ID) tunnel.append(node("p", "Local connector ID · " + r.Tunnel.ID, "system-caption"));
    if (inventory) {
      tunnel.append(node("p", "Cloudflare inventory · " + date(r.Cloud.At), "system-caption"));
      list(tunnel, connectors.map(c => [
        c.ID, c.Known ? badge("Recognized", "good") : badge("Unrecognized", "warn"),
        [...(c.Origins || []), c.Edges + " edge links", c.Version].filter(Boolean).join(" · ")
      ]), "No active connector reported by Cloudflare.");
    }

    const clients = section("Gateway clients", "Established TCP connections sampled every 15 seconds. One IP can represent several clients behind a proxy or NAT. Brief connections can fall between samples.");
    const peers = r.Peers || [];
    if (r.PeersAvailable) {
      const ipCount = new Set(peers.map(p => p.IP)).size;
      clients.append(node("p", ipCount + " distinct source IPs · " + peers.reduce((sum,p) => sum+p.Sockets,0) + " open connections"));
    } else clients.append(badge("Socket inventory unavailable", "warn"));
    clients.append(node("p", "Sessions with recorded usage in the last 15 minutes · " + (r.RecentSessions ?? "—"), "system-caption"));
    if (peers.length) list(clients, peers.map(p => peerRow(p, r.PeersAvailable, false)), "");
    clients.append(node("h3", "Recently seen"));
    list(clients, (r.Recent || []).map(p => peerRow(p, r.PeersAvailable, true)), "No sampled peers in the past 24 hours.");

    const accounts = section("Provider access", "Read-only provider checks every five minutes. Last check · " + date(r.AccountsAt) + " ICT");
    if (!r.AccountsAvailable) accounts.append(badge("Waiting for account inventory"));
    list(accounts, (r.Accounts || []).map(a => [
      a.Name, r.AccountsAvailable ? stateBadge(a.State) : badge("Last known · " + a.State),
      a.Provider + " · " + date(a.At)
    ]), "No provider check results.");

    const history = section("Incident history", "Service and tunnel readiness alerts trigger after three consecutive failures. Confirmed provider 401s and unrecognized connector IDs are reported on detection.");
    list(history, [...(r.Changes || [])].reverse().map(c => {
      const recovered = c.Message.endsWith(" · recovered");
      return [date(c.At), recovered ? badge("Recovered", "good") : badge("Alert", "warn"),
        recovered ? c.Message.slice(0, -" · recovered".length) : c.Message];
    }), "No incident transitions recorded.");
    const delivery = node("p", "Last Telegram delivery · ", "system-caption");
    if (r.LastDelivery) {
      const [state, at] = r.LastDelivery.split(" · ");
      delivery.append(badge(state, {Delivered: "good", Failed: "bad"}[state]), at ? " " + date(at) : "");
    } else delivery.append(badge("No delivery recorded"));
    history.append(delivery);
    health.replaceChildren(resources, services, accounts);
    const focusedRoute = connections.contains(document.activeElement) ? document.activeElement.href : null;
    connections.replaceChildren(routeMap(r), tunnel, clients);
    if (focusedRoute) {
      Array.from(connections.querySelectorAll(".route-node a")).find(a => a.href === focusedRoute)?.focus({preventScroll: true});
    }
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
    try{render(await api("/api/system"));}catch{el("monitor-time").replaceChildren(badge("Status unavailable", "warn"), " Retrying…");}finally{busy=false;}
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
