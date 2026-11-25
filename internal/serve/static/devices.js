(() => {
  "use strict";
  const panel = document.getElementById("device-sessions");
  if (!panel) return;
  const field = id => document.getElementById(id);
  const feedback = field("device-feedback");
  const dialog = field("device-dialog");
  const gateways = field("device-gateway");
  const clock = new Intl.DateTimeFormat("vi-VN", {timeZone:"Asia/Ho_Chi_Minh",dateStyle:"short",timeStyle:"medium"});
  const places = new Intl.DisplayNames(["en"], {type:"region"});
  const when = n => n ? clock.format(new Date(n * 1000)) : "—";
  function element(tag,text,css) {
    const el=document.createElement(tag);
    if(text!==undefined) el.textContent=text;
    if(css) el.className=css;
    return el;
  }
  async function api(path,body) {
    const opts={cache:"no-store"};
    if(body!==undefined) Object.assign(opts,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
    const res=await fetch("/api/system/devices"+path,opts);
    if(res.status===401){location.reload();throw Error("Sign in again");}
    if(!res.ok) throw Error((await res.json().catch(()=>({}))).error||"Device inventory unavailable");
    return res.json();
  }
  function badge(text,tone){
    const b=element("span",text,"monitor-badge");b.dataset.tone=tone;return b;
  }
  let data,loading=false;
  function section(title,rows) {
    const wrap=element("section",undefined,"report-panel");
    wrap.append(element("h2",title));
    if(!rows.length){wrap.append(element("p","No devices to display.","muted"));return wrap;}
    const scroll=element("div",undefined,"device-scroll"),table=element("table");
    const head=table.createTHead().insertRow();
    const web=rows[0].Kind==="web";
    for(const label of ["Device / client","Source IP","Location",web?"Last sign-in":"First authenticated",web?"Last seen":"Last authenticated","Status","Action"]){const th=element("th",label);th.scope="col";head.append(th);}
    const body=table.createTBody();
    for(const r of rows){
      const tr=body.insertRow(),identity=tr.insertCell();
      identity.append(element("strong",r.Name));
      const gateway=data.devices.find(x=>x.ID===r.Gateway);
      if(gateway) identity.append(element("small",gateway.Name));
      if(r.Agent) identity.append(element("small",r.Agent));
      tr.insertCell().textContent=r.IP||"—";
      tr.insertCell().textContent=r.Location?.match(/^[A-Z]{2}$/)?places.of(r.Location):r.Location||"—";
      tr.insertCell().textContent=when(r.Login);
      tr.insertCell().textContent=when(r.Seen);
      const revoked=r.Revoked||(r.Kind==="legacy"&&gateway?.Revoked);
      const cell=tr.insertCell();
      if(revoked) cell.append(badge("Revoked","bad"));
      else if(r.ID===data.current) cell.append(badge("This browser","good"));
      else if(r.Active) cell.append(badge(r.Active+" active request"+(r.Active>1?"s":""),"good"));
      else if(r.Seen && Date.now()/1000-r.Seen<120) cell.append(badge("Seen recently","good"));
      else cell.append(badge(r.Seen?"Idle":"Unused","neutral"));
      if(revoked)cell.append(element("small",when(revoked)));
      const actions=tr.insertCell();
      if(!revoked&&r.ID!==data.current){
        const btn=element("button",r.Kind==="legacy"?"Block source IP":"Revoke","btn danger sm");btn.type="button";
        btn.onclick=()=>revoke(r,btn);actions.append(btn);
      }
    }
    scroll.append(table);wrap.append(scroll);return wrap;
  }
  async function revoke(r,button) {
    const prompt=r.Kind==="share"?"Disable the shared ticket for "+r.Name+" on every IP? Individually issued tickets will keep working.":
      r.Kind==="legacy"?"Block "+r.IP+" from using this shared ticket? This affects everyone using it from that IP. If the ticket leaked, disable the shared ticket too.":
      "End access for "+r.Name+" ("+(r.IP||"unused ticket")+")?";
    if(!confirm(prompt))return;
    button.disabled=true;
    try{await api("/"+r.ID+"/revoke",{});await load();feedback.textContent="Access revoked.";}
    catch(e){feedback.textContent=e.message;button.disabled=false;}
  }
  function render(){
    const rows=data.devices;
    const shares=rows.filter(r=>r.Kind==="share");
    const disabledShares=new Set(shares.filter(r=>r.Revoked).map(r=>r.ID));
    const visible=field("device-show-revoked").checked ? rows : rows.filter(r=>!r.Revoked && !(r.Kind==="legacy" && disabledShares.has(r.Gateway)));
    gateways.replaceChildren(...shares.map(r=>new Option(r.Name,r.ID)));
    field("new-device-ticket").disabled=!shares.length;
    field("device-tables").replaceChildren(section("Admin browsers",visible.filter(r=>r.Kind==="web")),section("Gateway devices",visible.filter(r=>r.Kind==="ticket"||r.Kind==="legacy")));
    const controls=field("shared-ticket-controls");controls.replaceChildren();
    for(const g of shares){
      const row=element("p",g.Name+" · "+g.Endpoint+" ");
      if(g.Revoked) row.append(badge("Shared ticket disabled","bad"));
      else{const b=element("button","Disable shared ticket","btn danger sm");b.type="button";b.onclick=()=>revoke(g,b);row.append(b);}
      controls.append(row);
    }
  }
  async function load(){
    if(panel.hidden||document.hidden||loading||dialog.open)return;
    loading=true;
    try{data=await api("");render();feedback.textContent="Updated "+when(Date.now()/1000)+" ICT";}
    catch(e){feedback.textContent=e.message;}
    finally{loading=false;}
  }
  field("device-show-revoked").onchange=()=>{if(data)render();};
  field("device-refresh").onclick=load;
  field("new-device-ticket").onclick=()=>{
    field("device-name").value="";field("issued-ticket").value="";
    field("ticket-result").hidden=true;field("ticket-form").hidden=false;
    field("ticket-error").textContent="";dialog.showModal();field("device-name").focus();
  };
  dialog.addEventListener("close",()=>{field("issued-ticket").value="";load();});
  field("close-ticket").onclick=()=>dialog.close();
  field("ticket-form").onsubmit=async e=>{
    e.preventDefault();const button=field("issue-ticket");button.disabled=true;
    try{const res=await api("/tickets",{Gateway:gateways.value,Name:field("device-name").value.trim()});
      field("issued-ticket").value=res.ticket;field("ticket-result").hidden=false;field("ticket-form").hidden=true;field("issued-ticket").select();}
    catch(err){field("ticket-error").textContent=err.message;}
    finally{button.disabled=false;}
  };
  document.addEventListener("sectionchange",load); document.addEventListener("visibilitychange",load);
  setInterval(()=>{if(!panel.contains(document.activeElement)||document.activeElement===panel)load();},10000);load();
})();