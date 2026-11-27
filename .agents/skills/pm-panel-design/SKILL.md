---
name: pm-panel-design
description: Use before building or changing any UI of the Product Manager right panel (overview, hypothesis card, charts, path-check filmstrip, before/after). Visual rules, reference mockup and acceptance checklist.
---

# Product Manager right panel: visual design

Reference mockup: `design/pm-panel/index.html` (open in a browser, 4 tabs; `#overview`, `#hypothesis`, `#path`, `#after`). The reference contains a fully synthetic, internally consistent example. Copy the structure and visual language, then replace its synthetic values with authorized source data when available.

Goal: the jury understands each screen in 5 seconds without reading. The left Airlock chat says 3–5 lines; the right panel shows it.

## Principles

1. **One main thought per screen.** The title is the conclusion in one sentence, not an internal identifier or a section name like «Сигнал».
2. **A number instead of a paragraph.** Each claim is one large figure plus a small caption. Never show raw rows such as `self_service_order_index 100 index all` or prose paragraphs.
3. **Colour means something, and only that.** Red: bad, money is being lost. Amber: data blind spot. Green: confirmed. Grey: control and neutral UI. Only one accented thing per screen.
4. **Hierarchy by size, not by more headings.** Three levels: small uppercase label (11px, letter-spacing .06em, faint colour), title 19px/600, key figure 20–30px/650–700. Everything else 12–13px in muted colour.
5. **Always compare with control.** Every KES figure sits next to Astra Linux. On the chart: a dashed line "how KES would go if it moved like control" (baseline × control after/before) and the filled gap between it and the actual line. The gap is the story.
6. **Evidence on the chart, not in a list.** A pin on the point of the chart (П продажи, И интервью, К конкуренты, М Метрика) linked to a card below it; clicking either highlights the other. Show the source locator in small monospace at the bottom of the card.
7. **State by shape.** Lifecycle bar (найдено → исследуем → обосновано → в разработке → замеряем → подтверждено/отклонено), confidence meter (не доказано … подтверждено), ✓/○ chips per evidence type, a severity stripe on the left of each alert. Readable without reading.
8. **Sort by money.** Alerts and hypotheses are ordered by ₽ per week, the most expensive first. Cost of delay is shown as ₽/week plus a live counter "потеряно с обнаружения".
9. **Air and alignment.** Spacing through flex/grid `gap` (8 / 12 / 18px), one radius scale (8–12px), a 1px border only where an object is separate. Do not box everything.
10. **Numbers.** `font-variant-numeric: tabular-nums`; Russian format: decimal comma, space as thousands separator, «млн ₽», «−» (minus sign, not hyphen).
11. **One motion.** On open, the chart line draws in (stroke-dashoffset), then the gap and pins fade in; the ₽ counter ticks; the anomaly tile on the overview pulses. Nothing else moves. Respect `prefers-reduced-motion`.
12. **Real names only.** Kaspersky Endpoint Security, Astra Linux, Dr.Web, МойОфис, Kaspersky Small Office Security KL4141RCEFS. No demo/«учебный»/foo/technical names in visible text (see root AGENTS.md and `scripts/lint-placeholder-names.py`).

## Screens

- **Overview (morning):** status strip (products watched / deviations / ₽ at risk per week / broken metrics) → treemap by vendor or category (area = revenue, colour = deviation vs control, diverging red–neutral–green) → alerts by ₽/week. Click on an anomaly opens its hypothesis.
- **Hypothesis card:** label + conclusion title + cost-of-delay box on the right → lifecycle bar → target vs control chart with an update marker, counterfactual dashed line, filled gap, evidence pins → evidence cards → confidence meter + backlog task with the tag «сгенерирована ИИ-помощником».
- **Purchase path:** filmstrip of screenshots from the browser check, ✓/! stamp per frame, failing step with red border; click a frame → enlarged frame + «ждали / увидели / время / задача».
- **After release:** card turns green, cost of delay 0 ₽, before/after small multiples or overlay against the pre-release baseline.

## Implementation

- Colours as DaisyUI theme tokens; both dark and light must work. No hard-coded colours outside the token set.
- Charts are inline SVG, no new chart libraries. Treemap: squarified layout (~20 lines JS, see mockup).
- Keep labels inside the SVG viewBox; leave right margin for end-of-line values.
- Panel width ~700px; must also work at 400px (stack columns, filmstrip scrolls horizontally inside its own container).

## Acceptance checklist (before/after screenshots required)

- [ ] Can a person say what is wrong and how much it costs after 5 seconds on each screen?
- [ ] No paragraph longer than one line in the panel; no raw field names.
- [ ] Every target figure has its control next to it.
- [ ] Colours used only for meaning; one accent per screen.
- [ ] Numbers come from authorized data or the clearly marked synthetic arrays in the mockup. Values agree across all four screens.
- [ ] `python3 scripts/lint-placeholder-names.py` exits 0.
- [ ] Dark and light theme screenshots both readable.
