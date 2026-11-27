const MAX_TERMS = 5;
const geoAliases = {"RU-MOS":"москва подмосковье","RU-SPE":"санкт-петербург петербург спб","US":"сша америка usa","RU":"рф russia","":"мир worldwide"};
export function normalizeSearch(value) {
  return String(value || "").toLocaleLowerCase("ru").replaceAll("ё","е").normalize("NFKD").replace(/[\u0300-\u036f]/g,"").trim();
}
export function searchGeos(catalog, query, limit = 40) {
  const tokens = normalizeSearch(query).split(/\s+/).filter(Boolean);
  if (!tokens.length) {
    const wanted = ["RU","","RU-MOW","RU-MOS","RU-SPE","KZ","BY","US","DE","GB","TR","CN"];
    return { options: wanted.map(code => catalog.find(row => row[0] === code)).filter(Boolean), more: false };
  }
  const matches = catalog.filter(row => {
    const haystack = normalizeSearch(row[1] + " " + row[2] + " " + row[4] + " " + row[0] + " " + (geoAliases[row[0]] || ""));
    return tokens.every(token => haystack.includes(token));
  });
  const exact = normalizeSearch(query);
  matches.sort((a,b) => {
    const rank = row => normalizeSearch(row[1]) === exact ? 0 : normalizeSearch(row[1]).startsWith(exact) ? 1 : 2;
    return rank(a) - rank(b) || a[1].localeCompare(b[1],"ru");
  });
  return {options:matches.slice(0,limit),more:matches.length > limit};
}
function iso(date) { return date.toISOString().slice(0,10); }
export function periodDates(preset, today) {
  const date = new Date(today + "T12:00:00Z");
  if (!Number.isFinite(date.getTime())) return null;
  const y=date.getUTCFullYear(), m=date.getUTCMonth();
  const month = n => new Date(Date.UTC(y,m+n,1,12));
  if (preset==="12m" || preset==="3m" || preset==="1m") {
    const count = Number(preset.slice(0,-1));
    const end = month(0); end.setUTCDate(0);
    return {from:iso(month(-count)),to:iso(end)};
  }
  if (preset==="last-year") return {from:(y-1)+"-01-01",to:(y-1)+"-12-31"};
  return null;
}
export function canonicalRequest(value) {
  return JSON.stringify({
    mode:value.mode || "compare",
    terms:(value.terms || []).map(s => String(s).trim()),
    geo:value.geo || "",
    from:value.from || "",
    to:value.to || "",
    property:value.property || "web",
    category:value.category || "0",
    region:value.mode==="regions" ? (value.region || "") : ""
  });
}
export function closestPointIndex(clientX,left,width,count) {
  if (count<=1 || width<=0) return 0;
  return Math.max(0,Math.min(count-1,Math.round((clientX-left)/width*(count-1))));
}
function element(tag,className,text) {
  const node=document.createElement(tag);
  if(className)node.className=className;
  if(text!==undefined)node.textContent=text;
  return node;
}
const formStates=new WeakMap();
const chartStates=new Set();
let currentRecord=null;
function currentResult(){return document.querySelector("#results [data-result]");}
function readSnapshot(form) {
  const values = Array.from(form.querySelectorAll('input[name="terms"]'),input=>input.value.trim());
  const mode=form.querySelector('input[name="mode"]:checked')?.value || "compare";
  const selected=Number(form.querySelector('[name="related_term"]')?.value || 0);
  return {
    mode, terms:mode==="related" ? [values[selected] || ""] : values,
    geo:form.querySelector('[name="geo"]').value,
    from:form.querySelector('[name="from"]').value,
    to:form.querySelector('[name="to"]').value,
    property:form.querySelector('[name="property"]').value,
    category:form.querySelector('[name="category"]').value,
    region:mode==="regions" ? form.querySelector('[name="region"]').value : ""
  };
}
function setFieldMessage(form,text) {
  const node=form.querySelector("[data-form-error]");
  node.textContent=text || "";
  node.hidden=!text;
}
function updateDirty(form) {
  const record=currentResult();
  const state=formStates.get(form);
  const snapshot=readSnapshot(form);
  let dirty=false;
  if(record) {
    try{dirty=canonicalRequest(snapshot)!==canonicalRequest(JSON.parse(record.dataset.request));}
    catch{dirty=true;}
  }
  dirty=dirty || Boolean(state?.geoPending);
  form.dataset.dirty=String(dirty);
  const notice=document.querySelector("[data-draft-notice]");
  if(notice)notice.hidden=!record || !dirty;
  const badge=record?.querySelector("[data-result-stale]");
  if(badge)badge.hidden=!dirty;
  const label=form.querySelector("[data-submit-label]");
  if(label&&!state?.busy)label.textContent=record ? "Обновить сравнение" : "Показать интерес";
}
function refreshRelated(form) {
  const select=form.querySelector('[name="related_term"]');
  const previous=Number(select.value || 0);
  select.replaceChildren();
  Array.from(form.querySelectorAll('input[name="terms"]')).forEach((input,i) => {
    const option=element("option","",input.value.trim() || "Фраза "+(i+1));
    option.value=String(i);select.append(option);
  });
  select.value=String(Math.min(previous,Math.max(0,select.options.length-1)));
}
function refreshCards(form) {
  const cards=Array.from(form.querySelectorAll("[data-comparison-card]"));
  const mode=readSnapshot(form).mode;
  const min=mode==="compare"?2:1;
  cards.forEach((card,i)=>{
    card.dataset.series=String(i);
    const input=card.querySelector('[name="terms"]');
    input.setAttribute("aria-label","Поисковая фраза "+(i+1));
    const remove=card.querySelector("[data-remove-term]");
    remove.disabled=cards.length<=min;
    remove.setAttribute("aria-label","Удалить сравнение: "+(input.value.trim() || "фраза "+(i+1)));
    remove.title=cards.length<=min ? (min===2?"Для сравнения нужны две фразы":"Оставьте хотя бы одну фразу") : "Удалить фразу";
  });
  const add=form.querySelector("[data-add-term]");
  add.disabled=cards.length>=MAX_TERMS;
  add.hidden=cards.length>=MAX_TERMS;
  form.querySelector("[data-term-limit]").hidden=cards.length<MAX_TERMS;
  form.querySelector("[data-related-picker]").hidden=mode!=="related";
  form.querySelector("[data-region-level]").hidden=mode!=="regions";
  form.dataset.mode=mode;
  refreshRelated(form);
  updateDirty(form);
}
function validateForm(form,show=true) {
  const state=formStates.get(form), snapshot=readSnapshot(form);
  let message="";
  if(state.geoPending)message="Выберите регион из списка или нажмите Esc, чтобы оставить прежний.";
  else if(snapshot.terms.some(term=>!term))message="Введите название в каждую карточку или удалите пустую.";
  else if(snapshot.mode==="compare"&&snapshot.terms.length<2)message="Добавьте вторую фразу для сравнения.";
  else if(snapshot.terms.length>MAX_TERMS)message="Можно сравнить не больше пяти фраз за один запрос.";
  else if(snapshot.terms.some(term=>term.includes(",")))message="Одна карточка содержит одну фразу, без запятых.";
  else if(new Set(snapshot.terms.map(normalizeSearch)).size!==snapshot.terms.length)message="Эта фраза уже добавлена. Укажите разные поисковые фразы.";
  else if(!snapshot.from||!snapshot.to||snapshot.from>snapshot.to)message="Укажите начало и конец периода в правильном порядке.";
  if(show)setFieldMessage(form,message);
  return !message;
}
function addTerm(form) {
  const cards=form.querySelectorAll("[data-comparison-card]");
  if(cards.length>=MAX_TERMS){setFieldMessage(form,"Можно сравнить не больше пяти фраз.");return;}
  const last=cards[cards.length-1]?.querySelector('[name="terms"]');
  if(last&&!last.value.trim()){last.focus();setFieldMessage(form,"Сначала введите фразу в пустую карточку.");return;}
  const content=document.getElementById("comparison-template").content.cloneNode(true);
  form.querySelector("[data-comparison-list]").insertBefore(content,form.querySelector("[data-add-term]"));
  refreshCards(form);setFieldMessage(form,"");
  form.querySelectorAll('input[name="terms"]')[cards.length].focus();
}
function initializeGeo(form) {
  const state=formStates.get(form);
  const input=form.querySelector("[data-geo-search]");
  const hidden=form.querySelector('[name="geo"]');
  const menu=form.querySelector("[data-geo-menu]");
  const list=form.querySelector("[data-geo-list]");
  const hint=form.querySelector("[data-geo-hint]");
  const catalog=typeof GEO_CATALOG!=="undefined" ? GEO_CATALOG : [];
  let selected=catalog.find(row=>row[0]===hidden.value);
  if(!selected)selected=[hidden.value,input.value,"","",""];
  let options=[],active=0,open=false;
  const setOpen=value=>{
    open=value;menu.hidden=!value;input.setAttribute("aria-expanded",String(value));
    if(!value)input.removeAttribute("aria-activedescendant");
  };
  function highlight(index) {
    active=Math.max(0,Math.min(options.length-1,index));
    Array.from(list.children).forEach((option,i)=>{
      option.setAttribute("aria-selected",String(i===active));
      if(i===active){input.setAttribute("aria-activedescendant",option.id);option.scrollIntoView({block:"nearest"});}
    });
  }
  function render() {
    const query=state.geoPending ? input.value : "";
    const found=searchGeos(catalog,query);
    options=found.options;list.replaceChildren();input.removeAttribute("aria-activedescendant");
    options.forEach((row,i)=>{
      const option=element("button","geo-option");
      option.type="button";option.role="option";option.id="geo-option-"+i;
      option.dataset.geoValue=row[0];option.tabIndex=-1;
      option.append(element("span","geo-option-name",row[1]));
      const subtitle=row[3]==="region" ? "Регион · "+row[2] : row[3]==="world" ? "Все страны" : "Страна";
      option.append(element("span","geo-option-detail",subtitle));
      option.addEventListener("pointerdown",e=>e.preventDefault());
      option.addEventListener("click",()=>choose(row));
      list.append(option);
    });
    hint.textContent=options.length ? (found.more?"Показаны первые 40 вариантов. Уточните название.":"Названия регионов из Google Trends") : "Не нашли такой регион. Уточните название.";
    highlight(0);
  }
  function choose(row) {
    selected=row;hidden.value=row[0];input.value=row[1];state.geoPending=false;
    setOpen(false);setFieldMessage(form,"");input.focus({preventScroll:true});
    hidden.dispatchEvent(new Event("change",{bubbles:true}));
  }
  input.addEventListener("focus",()=>{setOpen(true);render();});
  input.addEventListener("click",()=>{if(!open){setOpen(true);render();}input.select();});
  input.addEventListener("input",()=>{
    state.geoPending=input.value!==selected[1];
    setOpen(true);render();updateDirty(form);
  });
  input.addEventListener("keydown",event=>{
    if(event.key==="ArrowDown"||event.key==="ArrowUp"){
      event.preventDefault();
      if(!open){setOpen(true);render();}else highlight(active+(event.key==="ArrowDown"?1:-1));
    }else if(event.key==="Enter"){
      event.preventDefault();event.stopPropagation();if(open&&options[active])choose(options[active]);
    }else if(event.key==="Escape"){
      event.preventDefault();input.value=selected[1];state.geoPending=false;setOpen(false);setFieldMessage(form,"");updateDirty(form);
    }else if(event.key==="Tab"){setOpen(false);}
  });
  document.addEventListener("pointerdown",event=>{
    if(!form.querySelector("[data-geo-control]").contains(event.target))setOpen(false);
  });
}
function applyPeriod(form) {
  const select=form.querySelector('[name="period_preset"]');
  const dates=periodDates(select.value,form.dataset.today);
  const custom=form.querySelector("[data-custom-dates]");
  custom.hidden=select.value!=="custom";
  if(dates){
    form.querySelector('[name="from"]').value=dates.from;
    form.querySelector('[name="to"]').value=dates.to;
  }
  updatePeriodCaption(form);updateDirty(form);
}
function updatePeriodCaption(form) {
  const snapshot=readSnapshot(form);
  const format=date=>{
    const value=new Date(date+"T12:00:00Z");
    return Number.isFinite(value.getTime())?new Intl.DateTimeFormat("ru",{day:"numeric",month:"short",year:"numeric",timeZone:"UTC"}).format(value):date;
  };
  form.querySelector("[data-period-caption]").textContent=format(snapshot.from)+" – "+format(snapshot.to);
}
function feedback(message,kind="error") {
  const box=document.getElementById("query-feedback");
  box.replaceChildren();
  const note=element("div",kind==="error"?"query-feedback query-feedback-error":"query-feedback query-feedback-loading");
  note.setAttribute(kind==="error"?"data-query-error":"data-query-loading","");
  note.role=kind==="error"?"alert":"status";
  note.append(element("span","",message));box.append(note);
}
function setBusy(form,busy) {
  const state=formStates.get(form);state.busy=busy;
  form.dataset.loading=String(busy);form.setAttribute("aria-busy",String(busy));
  const label=form.querySelector("[data-submit-label]");
  label.textContent=busy?"Получаем данные…":currentResult()?"Обновить сравнение":"Показать интерес";
  if(busy)feedback(currentResult()?"Обновляем сравнение. Ниже остаются предыдущие данные.":"Получаем данные Google Trends…","loading");
  else document.querySelector("[data-query-loading]")?.remove();
}
function initializeForm(form) {
  if(formStates.has(form))return;
  formStates.set(form,{busy:false,geoPending:false});
  form.dataset.enhanced="true";
  initializeGeo(form);
  form.querySelector("[data-add-term]").addEventListener("click",()=>addTerm(form));
  form.querySelector("[data-edit-dates]").addEventListener("click",()=>{form.querySelector('[name="period_preset"]').value="custom";form.querySelector("[data-custom-dates]").hidden=false;form.querySelector('[name="from"]').focus();updateDirty(form);});
  form.addEventListener("click",event=>{
    const remove=event.target.closest("[data-remove-term]");
    if(remove&&!remove.disabled){
      const card=remove.closest("[data-comparison-card]");
      const next=card.nextElementSibling?.querySelector('[name="terms"]')||card.previousElementSibling?.querySelector('[name="terms"]');
      card.remove();refreshCards(form);setFieldMessage(form,"");next?.focus();
    }
  });
  form.addEventListener("keydown",event=>{
    if(event.key==="Enter"&&event.target.matches('input:not([name="terms"]):not([data-geo-search])'))event.preventDefault();
    if(event.key==="Enter"&&event.target.matches('input[name="terms"]')){
      event.preventDefault();
      if(form.querySelectorAll("[data-comparison-card]").length<MAX_TERMS)addTerm(form);
      else {setFieldMessage(form,"Добавлено пять фраз. Для запроса нажмите кнопку обновления.");form.querySelector('button[type="submit"]').focus();}
    }
  });
  form.addEventListener("input",event=>{
    if(event.target.name==="terms"){
      refreshCards(form);
      const values=Array.from(form.querySelectorAll('[name="terms"]'),input=>normalizeSearch(input.value)).filter(Boolean);
      setFieldMessage(form,new Set(values).size<values.length?"Эта фраза уже добавлена. Укажите разные поисковые фразы.":"");
    }
    if(event.target.name==="from"||event.target.name==="to"){
      form.querySelector('[name="period_preset"]').value="custom";updatePeriodCaption(form);
    }
    updateDirty(form);
  });
  form.addEventListener("change",event=>{
    if(event.target.name==="period_preset")applyPeriod(form);
    if(event.target.name==="mode")refreshCards(form);
    updateDirty(form);
  });
  form.addEventListener("submit",event=>{
    if(!validateForm(form)){
      event.preventDefault();event.stopImmediatePropagation();
      form.querySelector("[data-form-error]").scrollIntoView({block:"nearest"});
    }
  },true);
  form.addEventListener("htmx:before:request",()=>setBusy(form,true));
  form.addEventListener("htmx:finally:request",()=>{setBusy(form,false);refreshCards(form);updateDirty(form);});
  form.addEventListener("htmx:error",event=>{
    const response=event.detail?.ctx?.response;
    if(!response)feedback("Не удалось обновить сравнение. Проверьте соединение и попробуйте еще раз. Предыдущие данные сохранены.");
  });
  refreshCards(form);updatePeriodCaption(form);
}
function initializeChart(root) {
  if(root.dataset.chartReady==="true")return;
  let data;
  try{data=JSON.parse(root.dataset.chart);}catch{return;}
  if(!data.points?.length)return;
  root.dataset.chartReady="true";
  const plot=root.querySelector("[data-chart-plot]");
  const stage=root.querySelector("[data-chart-stage]");
  const crosshair=root.querySelector("[data-crosshair]");
  const tooltip=root.querySelector("[data-chart-tooltip]");
  const tooltipDate=tooltip.querySelector("[data-tooltip-date]");
  const tooltipQuality=tooltip.querySelector("[data-tooltip-quality]");
  const tooltipValues=tooltip.querySelector("[data-tooltip-values]");
  const live=root.querySelector("[data-chart-live]");
  const markers=root.querySelector("[data-chart-markers]");
  const visibility=data.terms.map(()=>true);
  let selected=-1,pinned=false,dragging=false;
  const state={root,observer:null};
  chartStates.add(state);
  function drawTicks(){
    const axis=root.querySelector("[data-x-axis]");axis.replaceChildren();
    const count=plot.clientWidth<500?3:5;
    const indices=Array.from(new Set(Array.from({length:Math.min(count,data.points.length)},(_,i)=>Math.round(i*(data.points.length-1)/Math.max(1,Math.min(count,data.points.length)-1)))));
    for(const i of indices){
      const date=new Date(data.points[i].timestamp*1000);
      const label=element("span","",new Intl.DateTimeFormat("ru",{day:"numeric",month:"short",timeZone:"UTC"}).format(date));
      axis.append(label);
    }
  }
  function clear(){
    selected=-1;pinned=false;root.dataset.pinned="false";tooltip.hidden=true;crosshair.hidden=true;markers.replaceChildren();
    stage.removeAttribute("aria-valuetext");
  }
  function show(index,announce=false){
    selected=Math.max(0,Math.min(data.points.length-1,index));root.dataset.pinned=String(pinned);
    const point=data.points[selected],percent=data.points.length===1?0:selected/(data.points.length-1)*100;
    crosshair.hidden=false;crosshair.style.left=percent+"%";
    tooltip.hidden=false;tooltipDate.textContent=point.date;tooltipQuality.textContent=point.quality;
    tooltipQuality.dataset.partial=String(point.partial!==false);
    tooltipValues.replaceChildren();markers.replaceChildren();
    const spoken=[point.date,point.quality];
    point.values.forEach((value,i)=>{
      const row=element("div","point-value");row.dataset.series=String(i);row.dataset.visible=String(visibility[i]);
      const dot=element("span","series-dot");dot.setAttribute("aria-hidden","true");row.append(dot);
      const name=element("div","point-value-name",value.term);
      if(!visibility[i])name.append(element("small","","Скрыто на графике"));
      if(value.detail)name.append(element("small","",value.detail));
      row.append(name,element("strong","point-value-number",value.label));tooltipValues.append(row);
      spoken.push(value.term+": "+value.label+(value.detail?", "+value.detail:""));
      if(value.index!==null&&visibility[i]){
        const mark=element("span","chart-marker");mark.dataset.series=String(i);
        mark.dataset.uncertain=String(value.hasData!==true||point.partial!==false);
        mark.style.left=percent+"%";mark.style.top=(100-value.index)+"%";markers.append(mark);
      }
    });
    if(announce)live.textContent=spoken.join(". ");
    stage.setAttribute("aria-label","Интерес по интервалам. "+point.date+". Стрелки влево и вправо меняют интервал.");
    const left=percent/100*plot.clientWidth;
    const width=tooltip.offsetWidth||280;
    tooltip.style.setProperty("--tooltip-left",Math.max(8,Math.min(plot.clientWidth-width-8,left+18))+"px");
  }
  function pointerIndex(event){const rect=plot.getBoundingClientRect();return closestPointIndex(event.clientX,rect.left,rect.width,data.points.length);}
  plot.addEventListener("pointermove",event=>{
    if(event.pointerType==="mouse"||dragging)show(pointerIndex(event),false);
  });
  plot.addEventListener("pointerdown",event=>{
    if(event.button!==0&&event.pointerType==="mouse")return;
    pinned=true;dragging=true;stage.focus({preventScroll:true});show(pointerIndex(event),true);
    if(event.pointerType!=="mouse")plot.setPointerCapture(event.pointerId);
  });
  plot.addEventListener("pointerup",event=>{
    dragging=false;if(plot.hasPointerCapture(event.pointerId))plot.releasePointerCapture(event.pointerId);
  });
  plot.addEventListener("pointercancel",()=>{dragging=false;});
  plot.addEventListener("pointerleave",()=>{if(!pinned&&!dragging)clear();});
  stage.addEventListener("keydown",event=>{
    let next=selected<0?data.points.length-1:selected;
    if(event.key==="ArrowLeft")next=selected<0?data.points.length-1:next-1;
    else if(event.key==="ArrowRight")next=selected<0?0:next+1;
    else if(event.key==="Home")next=0;
    else if(event.key==="End")next=data.points.length-1;
    else if(event.key==="Escape"){event.preventDefault();clear();return;}
    else return;
    event.preventDefault();pinned=true;show(next,true);
  });
  root.querySelectorAll("[data-series-toggle]").forEach(button=>{
    const i=Number(button.dataset.seriesToggle);
    button.addEventListener("click",()=>{
      visibility[i]=!visibility[i];button.setAttribute("aria-pressed",String(visibility[i]));
      root.querySelectorAll('[data-chart-line="'+i+'"]').forEach(line=>{line.style.display=visibility[i]?"":"none";});
      root.querySelector("[data-chart-all-hidden]").hidden=visibility.some(Boolean);
      if(selected>=0)show(selected,true);
    });
  });
  root.querySelector("[data-tooltip-close]")?.addEventListener("click",clear);
  state.observer=new ResizeObserver(()=>{drawTicks();if(selected>=0)show(selected,false);});
  state.observer.observe(plot);drawTicks();
}
export function initializeExplorer(root=document) {
  document.querySelectorAll("[data-research-form]").forEach(initializeForm);
  for(const state of chartStates){
    if(!state.root.isConnected){state.observer?.disconnect();chartStates.delete(state);}
  }
  root.querySelectorAll?.("[data-chart]").forEach(initializeChart);
  const record=currentResult();
  if(record!==currentRecord){
    currentRecord=record;
    if(record)document.getElementById("query-feedback")?.replaceChildren();
  }
  document.querySelectorAll("[data-research-form]").forEach(updateDirty);
}
if(typeof document!=="undefined"){
  const boot=()=>initializeExplorer(document);
  if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",boot,{once:true});else boot();
  document.addEventListener("htmx:after:swap",boot);
}
