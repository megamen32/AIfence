// The server provides one set of comparable offers per chart.
(() => {
  if (customElements.get("market-price-history")) return;
  const svgNS = "http://www.w3.org/2000/svg";
  const observationGap = 6 * 60 * 60 * 1000;
  const number = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 2 });
  const minorPrice = new Intl.NumberFormat("ru-RU", {
    minimumFractionDigits: 2, maximumFractionDigits: 2
  });
  function money(value) {
    return Number.isInteger(value) ? number.format(value) : minorPrice.format(value);
  }
  const dates = new Intl.DateTimeFormat("ru-RU", { day: "2-digit", month: "short" });
  const times = new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit" });
  const stamp = new Intl.DateTimeFormat("ru-RU", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit"
  });
  function svg(tag, attrs, label) {
    const node = document.createElementNS(svgNS, tag);
    for (const [key, val] of Object.entries(attrs)) node.setAttribute(key, val);
    if (label !== undefined) node.textContent = label;
    return node;
  }
  function el(tag, cls, label) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (label !== undefined) node.textContent = label;
    return node;
  }
  function sourceURL(raw) {
    try {
      const u = new URL(raw);
      return /^(https?:)$/.test(u.protocol) ? u.href : "";
    } catch { return ""; }
  }
  function nearest(points, time) {
    let low = 0, high = points.length;
    while (low < high) {
      const mid = (low + high) >>> 1;
      if (points[mid].t < time) low = mid + 1; else high = mid;
    }
    const prev = points[low - 1], next = points[low];
    if (!next) return prev;
    if (!prev) return next;
    return time - prev.t <= next.t - time ? prev : next;
  }
  class MarketPriceHistory extends HTMLElement {
    // Calculate ticks in minor currency units so small spreads retain their full precision.
    static priceScale(min, max) {
      const low = Math.floor(min * 100 + 1e-7);
      const high = Math.ceil(max * 100 - 1e-7);
      const spread = high - low;
      const padding = spread > 0 ? Math.max(1, Math.round(spread / 10)) : Math.max(2, Math.round(high / 200));
      const lower = Math.max(0, low - padding);
      const upper = high + padding;
      const desired = Math.max(1, (upper - lower) / 5);
      const base = 10 ** Math.floor(Math.log10(desired));
      const step = [1, 2, 5, 10].map(factor => factor * base).find(candidate => candidate >= desired);
      const first = Math.floor(lower / step) * step;
      const last = Math.ceil(upper / step) * step;
      const ticks = [];
      const formatter = step < 100 ? minorPrice : number;
      for (let i = 0; i <= Math.round((last - first) / step); i++) {
        const value = (first + i * step) / 100;
        ticks.push({ value, label: formatter.format(value) });
      }
      return { min: first / 100, max: last / 100, ticks };
    }
    connectedCallback() {
      if (this.chart) { this.observe(); return; }
      let input;
      try { input = JSON.parse(this.dataset.series || "[]"); } catch { return; }
      if (!Array.isArray(input)) return;
      this.series = input.map((s) => ({
        label: s.label, store: s.store, visible: true,
        points: (s.points || []).map(p => ({
          t: Date.parse(p.at), v: p.price, url: sourceURL(p.url || s.url)
        })).filter(p => Number.isFinite(p.t) && Number.isFinite(p.v) && p.v >= 0)
           .sort((a, b) => a.t - b.t)
      })).filter(s => s.points.length);
      if (!this.series.length) return;
      this.unit = this.dataset.unit || "₽";
      const query = new URL(location.href).searchParams;
      const restore = Date.parse(query.get("at") || "");
      if (Number.isFinite(restore)) {
        this.selected = restore;
        const shown = (query.get("series") || "").split(",").filter(Boolean);
        if (shown.length && this.series.some(s => shown.includes(s.store))) {
          this.series.forEach(s => { s.visible = shown.includes(s.store); });
        }
      }
      this.legend = el("div", "mw-chart-legend");
      this.legend.setAttribute("aria-label", "Магазины на графике");
      this.chart = svg("svg", { role: "img", "aria-label": "Наблюдения цен за выбранный период" });
      this.chart.classList.add("mw-chart-plot");
      this.cursor = el("input", "mw-chart-range");
      this.cursor.type = "range";
      this.cursor.min = "0";
      this.cursor.step = "1";
      this.cursor.setAttribute("aria-label", "Выбрать дату наблюдения");
      this.detail = el("div", "mw-chart-detail");
      this.series.forEach((s, i) => {
        const button = el("button", "mw-chart-key", s.label);
        const last = s.points[s.points.length - 1];
        const price = el("strong", "mw-key-price", money(last.v) + " " + this.unit);
        price.title = "Последнее наблюдение " + stamp.format(last.t);
        button.append(price);
        button.setAttribute("aria-label", s.label + ", последнее наблюдение " + price.textContent);
        button.type = "button";
        button.dataset.store = s.store || "";
        button.dataset.index = String(i % 3);
        button.setAttribute("aria-pressed", String(s.visible));
        button.addEventListener("click", () => {
          s.visible = !s.visible;
          button.setAttribute("aria-pressed", String(s.visible));
          this.draw();
          this.publishSelection();
        });
        this.legend.append(button);
      });
      this.append(this.legend, this.chart, this.cursor, this.detail);
      this.cursor.addEventListener("input", () => { this.select(this.ticks[Number(this.cursor.value)]); this.publishSelection(); });
      const pointer = (event) => {
        if (!this.ticks.length) return;
        const bounds = this.chart.getBoundingClientRect();
        const x = (event.clientX - bounds.left) * this.width / bounds.width;
        const t = this.leftTime + (x - this.left) / (this.width - this.left - this.right) * this.duration;
        this.select(nearest(this.ticks, t));
      };
      this.chart.addEventListener("pointermove", e => { if (e.pointerType === "mouse") pointer(e); });
      this.chart.addEventListener("pointerdown", event => { pointer(event); this.publishSelection(); });
      this.draw();
      this.observe();
    }
    disconnectedCallback() { if (this.resize) this.resize.disconnect(); }
    observe() {
      if (this.resize) this.resize.disconnect();
      this.resize = new ResizeObserver(() => {
        if (Math.abs(this.getBoundingClientRect().width - this.width) > 1) this.draw();
      });
      this.resize.observe(this);
    }
    draw() {
      const active = this.series.filter(s => s.visible);
      this.width = Math.max(240, Math.round(this.getBoundingClientRect().width || 480));
      this.chart.setAttribute("viewBox", `0 0 ${this.width} 174`);
      this.chart.replaceChildren();
      this.ticks = Array.from(new Set(active.flatMap(s => s.points.map(p => p.t))))
        .sort((a,b) => a-b).map((t, idx) => ({ t, idx }));
      this.cursor.hidden = this.ticks.length < 2;
      if (!this.ticks.length) {
        this.detail.replaceChildren();
        this.chart.append(svg("text", {x: this.width / 2, y: 80, "text-anchor":"middle"}, "Выберите магазин"));
        return;
      }
      this.cursor.max = String(this.ticks.length - 1);
      const endpoints = [this.ticks[0].t, this.ticks[this.ticks.length-1].t];
      this.leftTime = endpoints[0]; this.duration = endpoints[1] - endpoints[0];
      if (!this.duration) { this.leftTime -= 3600000; this.duration = 7200000; }
      const points = active.flatMap(s => s.points);
      let min = Infinity, max = -Infinity;
      for (const p of points) { min = Math.min(min, p.v); max = Math.max(max, p.v); }
      const axis = MarketPriceHistory.priceScale(min, max);
      this.left = Math.max(42, ...axis.ticks.map(tick => tick.label.length * 7 + 12));
      this.right = 11;
      const x = t => this.left + (t-this.leftTime) / this.duration * (this.width-this.left-this.right);
      const y = v => 137 - (v - axis.min) / (axis.max - axis.min) * 117;
      for (const tick of axis.ticks) {
        const pos = y(tick.value);
        this.chart.append(svg("line",{x1:this.left,y1:pos,x2:this.width-this.right,y2:pos,class:"mw-gridline"}));
        this.chart.append(svg("text",{x:this.left-7,y:pos+4,"text-anchor":"end"},tick.label));
      }
      const clock = this.duration <= 24*3600000 ? times : dates;
      if (this.ticks.length === 1) {
        this.chart.append(svg("text",{x:x(endpoints[0]),y:161,"text-anchor":"middle"},stamp.format(endpoints[0])));
      } else {
        this.chart.append(svg("text",{x:this.left,y:161},clock.format(endpoints[0])));
        this.chart.append(svg("text",{x:this.width-this.right,y:161,"text-anchor":"end"},clock.format(endpoints[1])));
      }
      this.line = svg("line",{y1:10,y2:139,class:"mw-crosshair"});
      this.chart.append(this.line);
      this.series.forEach((s,i) => {
        if (!s.visible) return;
        const group = svg("g", {"data-store": s.store || "", "data-index":String(i % 3), class:"mw-chart-series"});
        let path = "", prev;
        for (const p of s.points) {
          path += (prev && p.t - prev.t <= observationGap ? " L" : " M")+x(p.t).toFixed(2)+","+y(p.v).toFixed(2);
          prev = p;
        }
        group.append(svg("path", {d:path,fill:"none","stroke-width":2.2}));
        const stride = Math.max(1,Math.floor(s.points.length/300));
        s.points.forEach((p,n) => {
          if (n % stride && n !== s.points.length-1 && p.t-s.points[n-1].t <= observationGap &&
            s.points[n+1].t-p.t <= observationGap) return;
          group.append(svg("circle",{cx:x(p.t),cy:y(p.v),r: this.ticks.length < 20 ? 4.3 : 3.0}));
        });
        this.chart.append(group);
      });
      this.xCoordinate = x;
      this.select(nearest(this.ticks, this.selected ?? endpoints[1]));
    }
    publishSelection() {
      if (!Number.isFinite(this.selected)) return;
      const url = new URL(location.href);
      const at = new Date(this.selected).toISOString();
      const series = [...new Set(this.series.filter(s => s.visible).map(s => s.store))];
      if (series.length) {
        url.searchParams.set("at",at);
        url.searchParams.set("series",series.join(","));
      } else {
        url.searchParams.delete("at"); url.searchParams.delete("series");
      }
      window.history.replaceState(window.history.state,"",url);
      this.dispatchEvent(new CustomEvent("marketwatch:selection", {bubbles:true, detail:{
        sku:url.searchParams.get("q"),at,series,from:url.searchParams.get("from"),to:url.searchParams.get("to"),
        qty:Number(url.searchParams.get("qty") || 1)
      }}));
    }
    select(tick) {
      if (!tick || !this.xCoordinate) return;
      this.selected = tick.t;
      this.cursor.value = String(tick.idx);
      this.cursor.setAttribute("aria-valuetext",stamp.format(tick.t));
      const x = this.xCoordinate(tick.t);
      this.line.setAttribute("x1",x); this.line.setAttribute("x2",x);
      const title = el("time","mw-chart-stamp",stamp.format(tick.t));
      title.dateTime = new Date(tick.t).toISOString();
      this.detail.replaceChildren(title);
      for (const s of this.series.filter(s => s.visible)) {
        const p = nearest(s.points,tick.t);
        const row = el("span","mw-chart-quote");
        row.append(el("span","mw-chart-seller",s.label));
        if (Math.abs(p.t - tick.t) > observationGap/2) {
          row.append(el("span","mw-chart-unobserved","Нет замера"));
        } else {
          const amount = el(p.url ? "a" : "b", "mw-chart-value", money(p.v)+" "+this.unit);
          amount.title = "Наблюдение "+stamp.format(p.t);
          if (p.url) { amount.href = p.url; amount.target = "_blank"; amount.rel = "noopener noreferrer"; }
          row.append(amount);
          if (p.t !== tick.t) row.append(el("time","mw-chart-nearby",times.format(p.t)));
        }
        this.detail.append(row);
      }
    }
  }
  customElements.define("market-price-history", MarketPriceHistory);
})();
