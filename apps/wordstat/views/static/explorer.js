const LAST_MONTH="2026-09";
const formStates=new WeakMap();
const chartStates=new Set();
let currentRecord=null;
function element(tag,classes="",text=""){const node=document.createElement(tag);if(classes)node.className=classes;if(text)node.textContent=text;return node;}
export function normalizeSearch(value){return String(value||"").toLocaleLowerCase("ru").replace(/ё/g,"е").replace(/[^\p{L}\p{N}]+/gu," ").trim();}
const regions=REGION_CATALOG.map(row=>({value:row[0],label:row[1],detail:row[2]||"",search:normalizeSearch(row.join(" "))}));
const regionMap=new Map(regions.map(row=>[row.value,row]));
export function searchRegions(query,limit=40){
 const q=normalizeSearch(query);
 if(!q){return ["225","","213","1","2","54","66","35","149"].map(id=>regionMap.get(id)).filter(Boolean).slice(0,limit);}
 const candidates=[];
 for(const row of regions){
  const label=normalizeSearch(row.label);
  let aliases="";if(label.includes("санкт петербург"))aliases+=" спб питер";if(label.includes("москов"))aliases+=" москва";if(row.value==="225")aliases+=" рф";
  if((row.search+aliases).includes(q))candidates.push({row,score:label===q?0:label.startsWith(q)?1:label.includes(q)?2:3});
 }
 candidates.sort((a,b)=>a.score-b.score||a.row.label.localeCompare(b.row.label,"ru"));
 return candidates.slice(0,limit).map(x=>x.row);
}
export function periodMonths(preset){
 if(preset==="2025")return {from:"2025-01",to:"2025-12"};
 const count=Number(preset);if(![3,6,12].includes(count))return null;
 const end=new Date(LAST_MONTH+"-01T00:00:00Z");const start=new Date(end);start.setUTCMonth(start.getUTCMonth()-count+1);
 return {from:start.toISOString().slice(0,7),to:LAST_MONTH};
}
export function canonicalRequest(request){
 const kind=request.kind||"top";
 const result={kind,phrase:String(request.phrase||"").trim()};
 if(kind==="regions"){result.region_mode=request.region_mode||"regions";return JSON.stringify(result);}
 const values=Array.isArray(request.region_ids)?request.region_ids:String(request.region_ids||"").split(",");
 const ids=values.filter(x=>String(x).trim()!=="").map(Number).filter(Number.isFinite);
 if(ids.length)result.region_ids=ids;
 result.device=request.device||"all";
 if(kind==="dynamics"){result.from_month=request.from_month||"";result.to_month=request.to_month||"";}
 return JSON.stringify(result);
}
function formRequest(form){
 return {kind:form.querySelector('[name="kind"]:checked')?.value||"top",phrase:form.querySelector('[name="phrase"]').value,region_ids:form.querySelector('[name="region_ids"]').value,device:form.querySelector('[name="device"]').value,region_mode:form.querySelector('[name="region_mode"]').value,from_month:form.querySelector('[name="from_month"]').value,to_month:form.querySelector('[name="to_month"]').value};
}
function resultNode(){return document.querySelector("[data-result]");}
function updateDirty(form){
 const record=resultNode();let dirty=false;
 if(record){try{dirty=canonicalRequest(formRequest(form))!==canonicalRequest(JSON.parse(record.dataset.request));}catch{dirty=true;}}
 const state=formStates.get(form);if(record&&state?.geoPending&&formRequest(form).kind!=="regions")dirty=true;
 document.querySelector("[data-draft-notice]").hidden=!dirty;
 const badge=record?.querySelector("[data-result-stale]");if(badge)badge.hidden=!dirty;
 const label=form.querySelector("[data-submit] > span:last-child");
 if(!state?.busy)label.textContent=record?"Обновить исследование":"Исследовать спрос";
}
function fieldMessage(form,message){const node=form.querySelector("[data-form-error]");node.textContent=message;node.hidden=!message;}
function feedback(message,loading=false){
 const target=document.getElementById("query-feedback");target.replaceChildren();
 const node=element("div","query-feedback "+(loading?"query-feedback-loading":"query-feedback-error"));node.dataset.loading=String(loading);
 if(loading){const spinner=element("span","loading loading-spinner loading-sm");spinner.setAttribute("aria-hidden","true");node.append(spinner);}
 node.append(element("p","",message));target.append(node);
}
function setBusy(form,busy){
 const state=formStates.get(form);state.busy=busy;
 form.setAttribute("aria-busy",String(busy));
 if(busy){form.querySelector("[data-submit] > span:last-child").textContent="Получаем данные";feedback("Получаем статистику. Предыдущий результат остаётся на экране.",true);}
 else {document.querySelector('#query-feedback [data-loading="true"]')?.remove();applyMode(form);}
}
function initializeGeo(form){
 const input=form.querySelector("[data-geo-search]"),hidden=form.querySelector('[name="region_ids"]'),menu=form.querySelector("[data-geo-menu]"),list=form.querySelector("#geo-options"),hint=form.querySelector("[data-geo-hint]");
 const state=formStates.get(form);
 let selected=regionMap.get(hidden.value)||{value:hidden.value,label:input.value};
 let matches=[],active=0,open=false;
 function close(){menu.hidden=true;input.setAttribute("aria-expanded","false");input.removeAttribute("aria-activedescendant");open=false;}
 function render(query){
  matches=searchRegions(query);active=0;list.replaceChildren();input.removeAttribute("aria-activedescendant");open=true;menu.hidden=false;input.setAttribute("aria-expanded","true");
  matches.forEach((row,i)=>{
   const option=element("button","geo-option");option.type="button";option.role="option";option.id="geo-option-"+i;
   option.setAttribute("aria-selected",String(i===active));option.append(element("span","geo-option-name",row.label));
   if(row.detail)option.append(element("small","geo-option-detail",row.detail));
   option.addEventListener("pointerdown",event=>event.preventDefault());option.addEventListener("click",()=>choose(row));list.append(option);
  });
  hint.textContent=matches.length===0?"Название не найдено. Попробуйте другую формулировку.":matches.length===40?"Уточните название: показаны первые 40 вариантов.":"Выберите название, чтобы применить его к следующему запросу.";
  highlight();
 }
 function highlight(){
  Array.from(list.children).forEach((option,i)=>option.setAttribute("aria-selected",String(i===active)));
  const option=list.children[active];if(option){input.setAttribute("aria-activedescendant",option.id);option.scrollIntoView({block:"nearest"});}
 }
 function choose(row){if(state.busy)return;selected=row;hidden.value=row.value;input.value=row.label;state.geoPending=false;fieldMessage(form,"");close();updateDirty(form);input.focus();}
 input.addEventListener("focus",()=>{if(!state.busy){input.select();render("");}});
 input.addEventListener("input",()=>{state.geoPending=input.value!==selected.label;render(input.value);updateDirty(form);});
 input.addEventListener("keydown",event=>{
  if(event.key==="Enter"){event.preventDefault();event.stopPropagation();if(open&&matches[active])choose(matches[active]);return;}
  if(event.key==="Escape"){event.preventDefault();input.value=selected.label;state.geoPending=false;close();fieldMessage(form,"");updateDirty(form);return;}
  if(event.key==="ArrowDown"||event.key==="ArrowUp"){event.preventDefault();if(!open)render("");else if(matches.length){active=(active+(event.key==="ArrowDown"?1:-1)+matches.length)%matches.length;highlight();}}
 });
 document.addEventListener("pointerdown",event=>{if(!input.closest(".geo-control").contains(event.target))close();});
}
function readableMonth(month){const value=new Date(month+"-01T00:00:00Z");return Number.isNaN(value.getTime())?month:new Intl.DateTimeFormat("ru",{month:"long",year:"numeric",timeZone:"UTC"}).format(value);}
function applyMode(form){
 const state=formStates.get(form),request=formRequest(form),kind=request.kind;
 form.querySelectorAll("[data-mode-fields]").forEach(group=>{const visible=group.dataset.modeFields.split(" ").includes(kind);group.hidden=!visible;group.disabled=!visible||state.busy;});
 const dates=form.querySelector("[data-custom-dates]");dates.hidden=kind!=="dynamics"||!state.customOpen;dates.disabled=kind!=="dynamics"||state.busy;
 form.querySelector("[data-edit-dates]").hidden=kind!=="dynamics";
 const hint=form.querySelector("[data-mode-hint]");
 hint.textContent=kind==="dynamics"?readableMonth(request.from_month)+" – "+readableMonth(request.to_month)+". Динамика поддерживает только оператор +.":kind==="regions"?"Все устройства. Точное историческое окно API не сообщает.":"Точное историческое окно API не сообщает. Частотности фраз могут пересекаться.";
 updateDirty(form);
}
function applyPeriod(form){
 const state=formStates.get(form),preset=form.querySelector("[data-period]").value;
 const months=periodMonths(preset);state.customOpen=preset==="custom";
 if(months){form.querySelector('[name="from_month"]').value=months.from;form.querySelector('[name="to_month"]').value=months.to;}
 applyMode(form);
}
function validate(form){
 const state=formStates.get(form),request=formRequest(form);
 if(state.busy)return false;
 if(!request.phrase.trim()){fieldMessage(form,"Введите поисковую фразу.");return false;}
 if(request.kind!=="regions"&&state.geoPending){fieldMessage(form,"Выберите регион из найденных вариантов или нажмите Esc, чтобы сохранить прежний.");form.querySelector("[data-geo-search]").focus();return false;}
 if(request.kind==="dynamics"&&(!/^\d{4}-\d{2}$/.test(request.from_month)||!/^\d{4}-\d{2}$/.test(request.to_month)||request.from_month>request.to_month)){fieldMessage(form,"Укажите месяцы: начало не позже конца периода.");return false;}
 fieldMessage(form,"");return true;
}
function setPhrase(phrase){
 const form=document.querySelector("[data-research-form]");if(!form||formStates.get(form)?.busy)return;
 const input=form.querySelector('[name="phrase"]');input.value=phrase;input.focus({preventScroll:true});input.scrollIntoView({block:"nearest"});fieldMessage(form,"");updateDirty(form);
}
function initializeForm(form){
 if(formStates.has(form))return;formStates.set(form,{busy:false,geoPending:false,customOpen:false});
 initializeGeo(form);
 form.querySelectorAll("[data-example]").forEach(button=>button.addEventListener("click",()=>setPhrase(button.dataset.example)));
 form.querySelector("[data-edit-dates]").addEventListener("click",()=>{formStates.get(form).customOpen=true;form.querySelector("[data-period]").value="custom";applyMode(form);form.querySelector('[name="from_month"]').focus();});
 form.addEventListener("keydown",event=>{if(event.key==="Enter"&&event.target.matches('input:not([data-geo-search]), select')){event.preventDefault();event.stopPropagation();}});
 form.addEventListener("input",event=>{if(event.target.name==="from_month"||event.target.name==="to_month"){form.querySelector("[data-period]").value="custom";applyMode(form);}fieldMessage(form,"");updateDirty(form);});
 form.addEventListener("change",event=>{if(event.target.name==="kind"){applyMode(form);fieldMessage(form,"");}else if(event.target.name==="period_preset")applyPeriod(form);updateDirty(form);});
 form.addEventListener("submit",event=>{if(!validate(form)){event.preventDefault();event.stopImmediatePropagation();}},true);
 form.addEventListener("htmx:before:request",()=>setBusy(form,true));
 form.addEventListener("htmx:before:response",event=>{const response=event.detail?.ctx?.response;if(response&&response.status>=400&&response.status!==401&&response.headers.get("X-Wordstat-Result")!=="error"){event.preventDefault();feedback("Не удалось обновить исследование. Источник или сервер временно недоступен. Предыдущие данные сохранены.");}});
 form.addEventListener("htmx:finally:request",()=>{setBusy(form,false);updateDirty(form);});
 form.addEventListener("htmx:error",event=>{if(!event.detail?.ctx?.response)feedback("Не удалось обновить исследование. Проверьте соединение и повторите запрос. Предыдущие данные сохранены.");});
 applyMode(form);
}
function initializeRanking(root){
 if(root.dataset.rankingReady==="true")return;root.dataset.rankingReady="true";
 const items=Array.from(root.querySelectorAll("[data-ranked-item]"));const input=root.querySelector("[data-rank-search]"),button=root.querySelector("[data-show-all]"),empty=root.querySelector("[data-rank-empty]");
 let expanded=false;
 function render(){
  const query=normalizeSearch(input?.value),matching=items.filter(item=>!query||normalizeSearch(item.dataset.label).includes(query));
  items.forEach(item=>{item.hidden=true;});matching.forEach((item,i)=>{item.hidden=!query&&!expanded&&i>=10;});
  empty.hidden=matching.length!==0;
  if(button){button.hidden=!!query||matching.length<=10;button.querySelector("span").textContent=expanded?"Свернуть список":"Показать все "+matching.length;button.setAttribute("aria-expanded",String(expanded));}
 }
 input?.addEventListener("input",render);input?.addEventListener("keydown",event=>{if(event.key==="Enter")event.preventDefault();});
 button?.addEventListener("click",()=>{expanded=!expanded;render();});
 root.querySelectorAll("[data-use-phrase]").forEach(button=>button.addEventListener("click",()=>setPhrase(button.dataset.usePhrase)));
 render();
}
export function closestPointIndex(x,left,width,count){return Math.max(0,Math.min(count-1,Math.round((x-left)/Math.max(1,width)*(count-1))));}
function initializeChart(root){
 if(root.dataset.chartReady==="true")return;
 let data;try{data=JSON.parse(root.dataset.chart);}catch{return;}if(!data.points?.length)return;
 root.dataset.chartReady="true";
 const plot=root.querySelector("[data-chart-plot]"),stage=root.querySelector("[data-chart-stage]"),crosshair=root.querySelector("[data-crosshair]"),tooltip=root.querySelector("[data-chart-tooltip]"),markers=root.querySelector("[data-chart-markers]"),live=root.querySelector("[data-chart-live]");
 let selected=-1,pinned=false,dragging=false;
 const state={root,observer:null};chartStates.add(state);
 function ticks(){
  const axis=root.querySelector("[data-x-axis]");axis.replaceChildren();
  const count=Math.min(plot.clientWidth<500?3:5,data.points.length);
  const indices=new Set(Array.from({length:count},(_,i)=>Math.round(i*(data.points.length-1)/Math.max(1,count-1))));
  for(const index of indices) {
   const point=data.points[index],date=new Date(point.month+"-01T00:00:00Z");
   axis.append(element("span","",new Intl.DateTimeFormat("ru",{month:"short",year:"2-digit",timeZone:"UTC"}).format(date)));
  }
 }
 function clear(){selected=-1;pinned=false;root.dataset.pinned="false";tooltip.hidden=true;crosshair.hidden=true;markers.replaceChildren();}
 function show(index,announce=false){
  selected=Math.max(0,Math.min(data.points.length-1,index));root.dataset.pinned=String(pinned);
  const point=data.points[selected],percent=data.points.length===1?50:selected/(data.points.length-1)*100;
  crosshair.hidden=false;crosshair.style.left=percent+"%";tooltip.hidden=false;
  tooltip.querySelector("[data-tooltip-date]").textContent=point.label;
  tooltip.querySelector("[data-tooltip-quality]").textContent=point.state==="observed"?"Запросы за полный календарный месяц":point.state==="ambiguous"?"Источник вернул несколько значений для месяца":"Источник не вернул значение за этот месяц";
  const values=tooltip.querySelector("[data-tooltip-values]");values.replaceChildren();markers.replaceChildren();
  const row=element("div","point-value");row.append(element("span","series-dot"),element("span","point-value-name",data.phrase),element("strong","point-value-number",point.count));values.append(row);
  if(point.y!==null){const marker=element("span","chart-marker");marker.style.left=percent+"%";marker.style.top=(100-point.y*100)+"%";markers.append(marker);}
  if(announce)live.textContent=point.label+". "+data.phrase+": "+point.count+(point.state==="observed"?" запросов.":".");
  stage.setAttribute("aria-label","Запросы по месяцам. "+point.label+". "+point.count+". Стрелки меняют месяц.");
  const width=tooltip.offsetWidth||290,left=percent/100*plot.clientWidth;
  tooltip.style.setProperty("--tooltip-left",Math.max(0,Math.min(plot.clientWidth-width,left+16))+"px");
 }
 function indexAt(event){const rect=plot.getBoundingClientRect();return closestPointIndex(event.clientX,rect.left,rect.width,data.points.length);}
 plot.addEventListener("pointermove",event=>{if(event.pointerType==="mouse"||dragging)show(indexAt(event));});
 plot.addEventListener("pointerdown",event=>{if(event.pointerType==="mouse"&&event.button!==0)return;pinned=true;dragging=true;stage.focus({preventScroll:true});show(indexAt(event),true);if(event.pointerType!=="mouse")plot.setPointerCapture(event.pointerId);});
 plot.addEventListener("pointerup",event=>{dragging=false;if(plot.hasPointerCapture(event.pointerId))plot.releasePointerCapture(event.pointerId);});
 plot.addEventListener("pointercancel",()=>{dragging=false;});
 plot.addEventListener("pointerleave",()=>{if(!pinned&&!dragging)clear();});
 stage.addEventListener("keydown",event=>{
  let next=selected<0?data.points.length-1:selected;
  if(event.key==="ArrowLeft")next=selected<0?data.points.length-1:next-1;
  else if(event.key==="ArrowRight")next=selected<0?0:next+1;
  else if(event.key==="Home")next=0;else if(event.key==="End")next=data.points.length-1;
  else if(event.key==="Escape"){event.preventDefault();clear();return;}else return;
  event.preventDefault();pinned=true;show(next,true);
 });
 root.querySelector("[data-tooltip-close]").addEventListener("click",clear);
 state.observer=new ResizeObserver(()=>{ticks();if(selected>=0)show(selected);});state.observer.observe(plot);ticks();
}
export function initializeExplorer(root=document){
 document.querySelectorAll("[data-research-form]").forEach(initializeForm);
 for(const state of chartStates){if(!state.root.isConnected){state.observer?.disconnect();chartStates.delete(state);}}
 root.querySelectorAll?.("[data-chart]").forEach(initializeChart);
 root.querySelectorAll?.("[data-ranking]").forEach(initializeRanking);
 const record=resultNode();if(record!==currentRecord){currentRecord=record;if(record)document.getElementById("query-feedback")?.replaceChildren();}
 document.querySelectorAll("[data-research-form]").forEach(updateDirty);
}
if(typeof document!=="undefined"){const boot=()=>initializeExplorer(document);if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",boot,{once:true});else boot();document.addEventListener("htmx:after:swap",boot);}
