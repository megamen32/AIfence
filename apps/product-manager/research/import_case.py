"""Build historical observations from the customer's selected source documents."""
import argparse
import hashlib
import json
import math
import os
import re
import tempfile
from datetime import date, timedelta
from functools import lru_cache
from io import BytesIO
from pathlib import Path
from statistics import mean
from zipfile import ZipFile
from xml.etree import ElementTree as ET

from xlsx import Workbook


DAY = timedelta(days=1)
WEEK = timedelta(days=7)


def stamp(value):
    if isinstance(value, (int, float)):
        return date(1899, 12, 30) + timedelta(days=value)
    return date.fromisoformat(str(value)[:10])


def one(folder, pattern):
    found = sorted(p for p in folder.glob(pattern) if not p.name.startswith('~$'))
    if len(found) != 1:
        raise ValueError(f'Expected one file matching {pattern}; found {len(found)}')
    return found[0]


@lru_cache(maxsize=16)
def revision(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()


def locator(sheet, cols, rows):
    spans = []
    for n in sorted(set(rows)):
        if spans and n == spans[-1][-1] + 1:
            spans[-1][1] = n
        else:
            spans.append([n, n])
    return ', '.join(f'{sheet}!{cols[0]}{a}:{cols[-1]}{b}' for a, b in spans)


def obs(ident, kind, product, metric, value, unit, start, end, path, place, **extra):
    return dict(id=ident, kind=kind, product_key=product, metric_key=metric,
                value=value, unit=unit, period={'from': str(start), 'to': str(end)},
                source_ref=dict(source_id=path.name, locator=place, revision=revision(path)),
                status='observed', **extra)


def batch(source, observations, cutoff, warnings=()):
    return dict(schema_version='aifence.v1', source=source, as_of=f'{cutoff}T23:59:59Z',
                status='partial' if warnings or not observations else 'ok',
                observations=observations, warnings=list(warnings))


def row_date(r, key):
    return stamp(r[key])


def read_weekly(rows, product, start, end, cutoff):
    kept = []
    seen = set()
    for n, row in rows.items():
        if row.get('B') != product:
            continue
        day = row_date(row, 'A')
        if not start <= day <= end or day > cutoff:
            continue
        if day.weekday() != 0 or day in seen:
            raise ValueError(f'Duplicate or non-Monday week: {product} {day}')
        seen.add(day)
        if not isinstance(row.get('C'), (int, float)) or not math.isfinite(row['C']):
            raise ValueError(f'Missing numeric weekly index: Продажи!C{n}')
        note = str(row.get('G', '')).lower()
        partial = 'неполн' in note or day + 6 * DAY > cutoff
        kept.append((n, day, row['C'], partial, row.get('G', '')))
    return sorted(kept, key=lambda x: x[1])


def observed_until(record, cutoff):
    end = min(cutoff, record[1] + 6 * DAY)
    if record[3]:
        match = re.search(r'по\s+(\d{2})\.(\d{2})\.(\d{4})',record[4])
        if match:
            day,month,year=map(int,match.groups())
            return min(end, date(year,month,day))
    return end


def sales(rows, path, products, base_start, base_end, post_start, cutoff):
    out, warnings = [], []
    for product in products:
        before = read_weekly(rows, product, base_start, base_end, cutoff)
        after = read_weekly(rows, product, post_start, cutoff, cutoff)
        for label, group in [('base', before), ('post', after)]:
            if not group:
                raise ValueError(f'Empty sales window {label}: {product}')
            first = base_start if label == 'base' else post_start
            last = base_end if label == 'base' else cutoff
            expected = {first + i * WEEK for i in range((last-first).days//7 + 1)}
            absent = sorted(expected - {row[1] for row in group})
            if absent:
                raise ValueError(f'Missing weekly index rows in {label}: {product}, {absent}')
        segment = 'Самостоятельные заказы клиентов'
        for label, left, right in [('full_weeks',[x for x in before if not x[3]],[x for x in after if not x[3]]),
                                    ('all_reported_weeks',before,after)]:
            if not left or not right or mean(x[2] for x in left) <= 0:
                raise ValueError(f'Not enough comparable indices for {product}')
            base = mean(x[2] for x in left)
            post = mean(x[2] for x in right)
            ns = ([x[0] for x in left], [x[0] for x in right])
            ref = '; '.join(locator('Продажи', 'AC', group) for group in ns)
            notes = [x[4] for x in left + right if x[3] and x[4]]
            out.append(obs(f'sales/{product}/{label}', 'metric', product,
                'family_orders_idx.relative_change_pct', round(100*(post/base-1), 3), 'percent',
                left[0][1], observed_until(right[-1],cutoff), path, ref, segment=segment,
                details=dict(method='100*(mean(post)/mean(base)-1)', variant=label,
                             baseline_mean=round(base,3), current_mean=round(post,3),
                             baseline_weeks=len(left), current_weeks=len(right),
                             baseline_from=str(left[0][1]), baseline_to=str(left[-1][1]+6*DAY),
                             current_from=str(right[0][1]), last_week_start=str(right[-1][1]),
                             partial_week_notes=notes),
                caveats=['Единица ряда: индекс недельного числа самостоятельных заказов. Абсолютные заказы, выручка и продажи через менеджеров в ряд не входят.']))
        if any(x[3] for x in after):
            warnings.append(f'{product}: в основной расчет входят полные недели. Дополнительный расчет включает помеченную неполную неделю.')
    return batch('sales', out, cutoff, warnings)


def events(releases, tasks, release_id, release_date, product, keyword, cutoff, path):
    out = []
    related_tasks=set()
    for i,r in releases.items():
        if i <= 1 or not r.get('B'):
            continue
        d=row_date(r,'B')
        if d > cutoff or abs((d-release_date).days)>7:
            continue
        if r['A']==release_id:
            related_tasks.update(re.findall(r'T-\d+',str(r.get('E',''))))
        out.append(obs('release/'+r['A'],'release',product,'release_log',r['D'],'text',
                       d,d,path,locator('Релизы','AG',[i]),details=dict(
                           related_task_ids=r.get('E'), affected_metrics=r.get('F'), release_type=r.get('C'))))
    b1 = batch('release',out,cutoff)
    out = []
    for i,r in tasks.items():
        if i<=1 or not r.get('A') or not r.get('F'):
            continue
        if r['A'] not in related_tasks and keyword.casefold() not in str(r.get('I','')+' '+r.get('B','')).casefold():
            continue
        d = row_date(r,'F')
        if d > cutoff:
            continue
        out.append(obs('task/'+r['A'],'task',product,'backlog_item',r['B'],'text',d,cutoff,path,
                       locator('Бэклог_задачи','AK',[i]),details=dict(
                           state=r.get('D'),priority=r.get('E'),description=r.get('I'),
                           release_id=r.get('H'),closed_date=r.get('G'),metric_ids=r.get('J'))))
    return [b1,batch('backlog',out,cutoff)]


def metrika(rows, definitions, product, keyword, release_id, start, cutoff, path):
    # Only aggregate separately by source segment, from the actual event numerator/denominator.
    out = []
    groups = {}
    def row_matches(r):
        return keyword.casefold() in str(r.get('C','')).casefold() and r.get('F') == release_id
    for i,r in rows.items():
        if not row_matches(r) or r.get('B') != 'cfg_reach_rate' or not r.get('J'):
            continue
        d=row_date(r,'A')
        if start<=d<=cutoff:
            details=json.loads(r['J'])
            counts=(details.get('configurator_visits'),details.get('family_visits'))
            if all(isinstance(x,(int,float)) for x in counts) and 0<=counts[0]<=counts[1]:
                groups.setdefault(r['D'],[]).append((i,d,*counts))
    for seg,items in sorted(groups.items()):
        a=sum(x[2] for x in items)
        b=sum(x[3] for x in items)
        if not b:
            continue
        out.append(obs('metrika/reach/'+seg,'metric',product,'cfg_reach_rate.observed_rows',round(a/b,6),'ratio',
                       min(x[1] for x in items),max(x[1] for x in items),path,
                       locator('Метрики_данные','AJ',[x[0] for x in items]),
                       segment=seg,numerator=a,denominator=b,sample_size=int(b),
                       details={'reported_days':len({x[1] for x in items})},
                       caveats=['Отношение сумм по представленным строкам своего сегмента. Дни без строк не считаются нулями.']))
    source_def = [(i,r) for i,r in definitions.items() if r.get('A')=='cfg_add_to_cart_rate']
    has_cart = any(row_matches(r) and r.get('B')=='cfg_add_to_cart_rate' and isinstance(r.get('G'),(int,float))
                   and start <= row_date(r,'A') <= cutoff for r in rows.values())
    if not has_cart and source_def:
        i,r=source_def[0]
        v=obs('metrika/cart/missing','metric',product,'cfg_add_to_cart_rate',None,'ratio',start,cutoff,
              path,locator('Метрики_справочник','AJ',[i]),caveats=[str(r.get('J',''))],
              details={'query':f'Метрики_данные: {keyword}, {release_id}, cfg_add_to_cart_rate, {start}..{cutoff}'})
        v['status']='missing'
        out.append(v)
    return batch('metrika',out,cutoff,['Источник: историческая выгрузка Яндекс.Метрики. Уровень покрытия зависит от дня и устройства.'])

def prices(rows, product, keyword, cutoff, path):
    out=[]
    for i,r in sorted(rows.items()):
        if keyword.casefold() not in str(r.get('B','')).casefold() or r.get('E')!='Да':
            continue
        if not (isinstance(r.get('I'),(int,float)) and isinstance(r.get('J'),(int,float)) and r.get('N')):
            continue
        d=stamp(r['N'])
        if d>cutoff:
            continue
        sku=str(r.get('C','')).strip()
        if not sku:
            continue
        out.append(obs(f'price/{i}','offer',product,'historical_competitor_price',r['I'],'RUB',d,d,path,
                       locator('03_Цена','AN',[i]),currency='RUB',tax_basis='unknown',
                       comparison_key=sku,details=dict(seller=r['A'],store_price=r['J'],
                           matched_by_source=r['E'],edition=r.get('G'),term_quantity=r.get('H'),
                           product_label=r['B'],url=r.get('F'))))
    return batch('market',out,cutoff,['Только сопоставимые предложения по классификации исследования. Даты проверки указаны в строках.'])


def interviews(archive, keyword, aliases, product, cutoff):
    # Design and result summaries can contain analyst interpretations. Select only identified interview sheets.
    allowed = {
        'E-com': {'Активные','Малоактивные','Новички','Отток'},
        'Отток клиентов': {'CustDev_Micro','CustDev_Small','CustDev_Medium'},
        'Сервисные преимущества': {'Small_Результаты анализа','Medium_Результаты анализа'},
    }
    terms=[t.casefold() for t in [keyword,*aliases]]
    out=[]
    with ZipFile(archive) as bundle:
        for name in sorted(bundle.namelist()):
            filename = Path(name).name
            prefix = next((k for k in allowed if filename.startswith(k)),None)
            if not prefix or not name.endswith('.xlsx') or name.startswith('__MACOSX'):
                continue
            book=Workbook(BytesIO(bundle.read(name)))
            try:
                for sheet in sorted(allowed[prefix] & set(book.names)):
                    rows=book.rows(sheet)
                    for i,r in sorted(rows.items()):
                        if i<=1:
                            continue
                        for col,text in sorted(r.items()):
                            if not isinstance(text,str) or not any(t in text.casefold() for t in terms):
                                continue
                            if not any(t in text.casefold() for t in ('менеджер','общени','онлайн','покупа','цен','выбор','продле')):
                                continue
                            if '@' in text or re.search(r'\+\d[\d ()-]{8,}',text):
                                continue
                            match=min(text.casefold().index(t) for t in terms if t in text.casefold())
                            lo=max(0,match-85)
                            hi=min(len(text),match+210)
                            excerpt=('…' if lo else '')+text[lo:hi]+('…' if hi<len(text) else '')
                            out.append(obs(f'interview/{prefix}/{sheet}/{col}{i}','research',product,
                                'interview_excerpt',excerpt,'text',cutoff,cutoff,archive,
                                f'{filename} / {sheet}!{col}{i}', details=dict(
                                    period_basis='Дата среза материалов. Дата интервью в ячейке не установлена.',
                                    source_level='Запись исследования по названию бренда; редакцию и принадлежность ответа к семейству следует сверить по источнику.')))
            finally:
                book.close()
    return batch('research',out,cutoff,['Совпадения по названию в интервью не оценивают частоту мнения у всех покупателей.'])


def market_context(slides, product, cutoff):
    out=[]
    # Only descriptive comparison slides, no hypotheses or recommendations.
    criteria={11:'Цена.', 14:'Покупка.'}
    with ZipFile(slides) as deck:
        for n,prefix in criteria.items():
            root=ET.fromstring(deck.read(f'ppt/slides/slide{n}.xml'))
            labels=[t.text for t in root.findall('.//{*}t') if t.text]
            if len(labels)<2 or not labels[0].startswith(prefix):
                continue
            out.append(obs(f'market/slide/{n}','research',product,'store_context',
                       labels[1],'text',cutoff,cutoff,slides,f'слайд {n}, подзаголовок',
                       details={'scope':'Вся выборка магазинов и товаров исследования.',
                           'period_basis':'Дата среза материалов, а не дата проверки отдельного магазина.'}))
    return batch('market',out,cutoff)


def choose_target(rows, releases, cutoff, product=None, control=None, release_id=None):
    families=sorted({r['B'] for n,r in rows.items() if n>1 and r.get('B')})
    if product and product not in families:
        raise ValueError('Product family is absent from sales')
    if control and control not in families:
        raise ValueError('Comparison family is absent from sales')
    candidates=[]
    for i,r in releases.items():
        if i<=1 or not r.get('B'):
            continue
        if release_id:
            if r.get('A')!=release_id:
                continue
        elif 'family_orders_idx' not in str(r.get('F','')).split(', '):
            continue
        d=row_date(r,'B')
        monday=d-timedelta(days=d.weekday())
        for family in ([product] if product else families):
            # Do not attribute an unexplained co-occurrence to a product:
            # require the release log to name its family/brand.
            if family.split()[0].casefold() not in str(r.get('D','')).casefold():
                continue
            before=read_weekly(rows,family,monday-13*WEEK,monday-DAY,cutoff)
            after=read_weekly(rows,family,monday+WEEK,cutoff,cutoff)
            left=[x[2] for x in before if not x[3]]
            right=[x[2] for x in after if not x[3]]
            if len(left)>=4 and len(right)>=4 and mean(left)>0:
                candidates.append((mean(right)/mean(left)-1,family,r['A'],d))
    if not candidates:
        raise ValueError('No eligible release with named sales family and four complete weeks on either side')
    change,target,rel,d=min(candidates)
    if not release_id and change>=0:
        raise ValueError('No eligible sales decline in the source period')
    if not control:
        alternatives=[f for f in families if f!=target]
        if len(alternatives)!=1:
            raise ValueError('Supply --control to choose a comparison family')
        control=alternatives[0]
    if target==control:
        raise ValueError('Choose a different comparison family')
    return target,control,rel,d


def build(args):
    folder=args.materials
    dataset=one(folder,'Датасет*.xlsx')
    market=one(folder,'Store*.xlsx')
    archive=one(folder,'Транскрибации интервью.zip')
    slides=one(folder,'Конкурентный_анализ*.pptx')
    wb=Workbook(dataset)
    try:
        releases=wb.rows('Релизы')
        orders=wb.rows('Продажи')
        metrics=wb.rows('Метрики_данные')
        cutoff=max(row_date(r,'A') for n,r in metrics.items() if n>1 and r.get('A'))
        product,control,release,release_date=choose_target(orders,releases,cutoff,args.product,args.control,args.release)
        keyword=args.keyword or product.split()[0]
        aliases=args.aliases if args.aliases is not None else (['касперск'] if keyword.casefold()=='kaspersky' else [])
        monday=release_date-timedelta(days=release_date.weekday())
        first=stamp(args.baseline_from) if args.baseline_from else monday - 13*WEEK
        if first.weekday()!=0 or first>=monday or monday+WEEK>cutoff:
            raise ValueError('Expected weeks before and after release, with a Monday baseline start')
        products=[product,control]
        batches=[sales(orders,dataset,products,first,monday-DAY,monday+WEEK,cutoff)]
        batches+=events(releases,wb.rows('Бэклог_задачи'),release,release_date,
                        product,keyword,cutoff,dataset)
        batches.append(metrika(metrics,wb.rows('Метрики_справочник'),product,keyword,
                              release,monday+WEEK,cutoff,dataset))
    finally:
        wb.close()
    wb=Workbook(market)
    try:
        batches.append(prices(wb.rows('03_Цена'),product,keyword,cutoff,market))
    finally:
        wb.close()
    batches.append(interviews(archive,keyword,aliases,product,cutoff))
    batches.append(market_context(slides,product,cutoff))
    return dict(schema_version='aifence.case.v1',source_policy='local_project_only',as_of=f'{cutoff}T23:59:59Z',batches=batches)


def write_json(path, obj):
    if path.resolve().is_relative_to(Path(__file__).resolve().parents[1]):
        raise ValueError('Choose a private output outside the deployable application directory')
    path.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.NamedTemporaryFile(mode='w',encoding='utf-8',dir=path.parent,delete=False) as temp:
        try:
            json.dump(obj,temp,ensure_ascii=False,allow_nan=False,separators=(',',':'))
            temp.write('\n')
            temp.close()
            os.replace(temp.name,path)
        finally:
            if os.path.exists(temp.name):
                os.unlink(temp.name)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--materials',required=True,type=Path)
    p.add_argument('--output',required=True,type=Path)
    p.add_argument('--product',help='Exact sales family; default selects the largest eligible decline')
    p.add_argument('--control',help='Comparison family; inferred only when exactly one other family exists')
    p.add_argument('--release',help='Filter by release ID, otherwise derive candidates from the release log')
    p.add_argument('--keyword',help='Source keyword; defaults to the first word of the chosen family')
    p.add_argument('--alias',dest='aliases',action='append',help='Extra spelling used in interviews; repeatable.')
    p.add_argument('--baseline-from',help='Monday starting the baseline window (default: 13 weeks before release week)')
    args=p.parse_args()
    obj=build(args)
    write_json(args.output,obj)
    print(' '.join(f"{b['source']}={len(b['observations'])}" for b in obj['batches']))


if __name__=='__main__':
    main()
