(() => {
  "use strict";
  const el = id => document.getElementById(id);
  const month = el("report-month");
  const compact = new Intl.NumberFormat("en", {notation:"compact", maximumFractionDigits:2});
  const exact = new Intl.NumberFormat("en");
  const parts = [["input","Uncached input"],["cacheRead","Cache reads"],["cacheWrite","Cache writes"],["output","Output"]];
  const total = counts => parts.reduce((sum,[key]) => sum + counts[key], 0);
  const node = (tag, text, cls) => {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (cls) element.className = cls;
    return element;
  };

  function metric(id, number) {
    el(id).textContent = compact.format(number);
    el(id).title = exact.format(number);
  }

  let selectedDay;
  function drawDays(data) {
    const chart = el("token-chart");
    chart.replaceChildren();
    el("day-detail").textContent = "";
    if (!data.firstDay || data.firstDay.slice(0,7) > data.month) return;
    const [year, m] = data.month.split("-").map(Number);
    const n = new Date(Date.UTC(year, m, 0)).getUTCDate();
    const peak = Math.max(1,...Object.values(data.days).map(total));
    const bars = node("div",undefined,"chart-bars");
    const labels = node("div",undefined,"axis-labels");
    const end = data.date.startsWith(data.month) ? Number(data.date.slice(-2)) : n;
    if (!selectedDay || !selectedDay.startsWith(data.month)) selectedDay = data.month + "-" + String(end).padStart(2,"0");
    for (let i=1; i<=n; i++) {
      const day = data.month + "-" + String(i).padStart(2,"0");
      const count = data.days[day];
      const amount = count ? total(count) : 0;
      const bar = node("button",undefined,"chart-day");
      bar.type = "button";
      bar.disabled = day < data.firstDay || day > data.date;
      const label = day + " · " + exact.format(amount) + " tokens · " + exact.format(count?.requests || 0) + " metered requests";
      bar.title = bar.disabled ? day + " · Outside recorded history" : label;
      bar.setAttribute("aria-label",bar.title);
      const fill = node("span");
      fill.style.height = Math.max(amount ? 2 : 0,100*amount/peak) + "%";
      bar.append(fill);
      function select() {
        selectedDay = day;
        for (const b of bars.children) b.setAttribute("aria-pressed",String(b === bar));
        el("day-detail").textContent = label;
      }
      bar.addEventListener("click",select);
      bar.setAttribute("aria-pressed",String(day===selectedDay));
      if (day===selectedDay && !bar.disabled) el("day-detail").textContent = label;
      bars.append(bar);
      labels.append(node("span",i === 1 || i % 7 === 0 || i === n ? i : ""));
    }
    chart.append(bars,labels);
  }

  function render(data) {
    if (!month.value) month.value = data.month;
    metric("usage-today",total(data.today));
    metric("usage-month",total(data.monthTotal));
    metric("usage-requests",data.monthTotal.requests);
    el("usage-today-note").textContent = data.date;
    el("usage-month-note").textContent = data.month;
    el("usage-coverage").textContent = data.firstDay
      ? "Earliest record " + data.firstDay + " · Select a day for exact counts."
      : "Waiting for the first metered response from the gateway.";
    drawDays(data);
    const mix=el("token-mix");
    mix.replaceChildren();
    const sum=total(data.monthTotal);
    for (const [key,label] of parts) {
      const row=node("div",undefined,"meter-row");
      const count=data.monthTotal[key];
      row.append(node("span",label),node("strong",exact.format(count)));
      const track=node("progress"); track.max=100; track.value=sum ? count/sum*100 : 0; track.setAttribute("aria-label",label);
      row.append(track); mix.append(row);
    }
    const models=el("model-usage"); models.replaceChildren();
    const ranking=Object.entries(data.models).sort((a,b)=>total(b[1])-total(a[1]) || a[0].localeCompare(b[0]));
    if (!ranking.length) { models.append(node("p","No metered responses in this month.","muted")); return; }
    const list=node("ul",undefined,"model-list");
    for (const [name,c] of ranking) {
      const row=node("li");
      row.append(node("strong",name),node("span",exact.format(c.requests)+" requests"),node("b",exact.format(total(c))+" tokens"));
      list.append(row);
    }
    models.append(list);
  }

  let pending;
  async function refresh() {
    if (pending) pending.abort();
    const req = new AbortController();
    pending = req;
    try {
      const res = await fetch("/api/usage" + (month.value ? "?month="+encodeURIComponent(month.value) : ""), {signal:req.signal,cache:"no-store"});
      if (!res.ok) throw new Error();
      const data = await res.json();
      if (req===pending) render(data);
    } catch (e) {
      if (e.name!=="AbortError") {
        for (const id of ["usage-today","usage-month","usage-requests"]) el(id).textContent="—";
        for (const id of ["token-chart","token-mix","model-usage","day-detail","usage-today-note","usage-month-note"]) el(id).replaceChildren();
        el("usage-coverage").textContent="Usage unavailable. Retrying automatically.";
      }
    }
  }
  month.addEventListener("change",refresh);
  document.addEventListener("visibilitychange",()=>{if(!document.hidden) refresh();});
  setInterval(()=>{if(!document.hidden) refresh();},60000);
  refresh();
})();
