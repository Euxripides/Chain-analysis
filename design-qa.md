# Fund Flow Design QA

**Source visual truth**

- `E:\codex\etl\v149-graph-v3-p1.png`
- Source pixels: 1536 × 960. The source was used as an architectural and interaction-density reference, not as a pixel-identical brand clone.

**Rendered implementation**

- Desktop: `C:\Users\Euripides\.codex\visualizations\2026\09\09\01a0855e-9639-7e01-bdeb-9d454da08958\fund-flow-desktop.png`
- Mobile: `C:\Users\Euripides\.codex\visualizations\2026\09\09\01a0855e-9639-7e01-bdeb-9d454da08958\fund-flow-mobile.png`
- Combined comparison: `C:\Users\Euripides\.codex\visualizations\2026\09\09\01a0855e-9639-7e01-bdeb-9d454da08958\fund-flow-comparison.png`
- Desktop viewport/pixels: 1440 × 900, device scale factor 1.
- Mobile viewport/pixels: 390 × 844, device scale factor 1.
- State: TRON real-data profile, 大额流向 lens, selected outbound relationship on desktop; canvas-focused responsive state on mobile.

**Full-view comparison evidence**

- Both views use the same dense workbench hierarchy: compact command header, left analysis context and lenses, central dotted graph canvas, right relationship inspector, minimap/controls, legend, and bottom status strip.
- The implementation intentionally retains the Blockchain Platform shell and tokens. The ETL reference has more nodes because it shows a previously expanded multi-hop investigation; the current API truth is one-hop Top 100 aggregation.
- The implementation preserves direction color semantics and animated dashed flow paths while avoiding unsupported labels and multi-hop claims.

**Focused region comparison evidence**

- Header: chain/address positioning, global/upstream/downstream modes, and depth controls match the reference hierarchy; unsupported depths are visibly disabled.
- Left panel: mapped investigation context, lenses, asset filter, ratio threshold, and evidence boundary are compact and aligned.
- Canvas: the post-fix capture shows all filtered nodes inside the viewport, a centered root, incoming/outgoing separation, directional arrows, controls, and minimap.
- Inspector: selected relationship shows its complete counterparty address, amount, timing, ratio, and transaction-hash evidence.

**Findings**

- No actionable P0/P1/P2 issues remain.
- Typography: existing UI and monospace tokens are consistent; dense labels remain legible at the target desktop viewport.
- Spacing/layout: three-pane desktop proportions and canvas hierarchy match the reference pattern; mobile converts the inspector to an overlay and starts canvas-first.
- Colors/tokens: blue root, teal upstream, and amber downstream are consistently applied with sufficient contrast.
- Image/asset quality: the graph uses native XYFlow vectors and Lucide icons; no raster placeholders or approximate logos were introduced.
- Copy/content: “智能调查” and “资金分析图” are separate concepts; mapping copy describes context transfer without implying they share a page.

**Comparison history**

1. Initial post-deploy capture: P1 — asynchronously loaded nodes were outside the fitted viewport, leaving mostly edge labels visible.
2. Fix: remount XYFlow when address/filter state changes so `fitView` runs against the actual node set; mobile defaults to a closed inspector.
3. Post-fix evidence: desktop and mobile captures show the complete filtered graph with no clipped persistent controls. No P0/P1/P2 findings remain.
4. Motion parity pass: added directional money-flow animation, staged node reveal, focused-path acceleration/glow, unrelated-path dimming, viewport-interaction pause, live-state pulse, and reduced-motion fallback.
5. Motion regression found: animating the React Flow node transform overrode graph positioning. The node animation was changed to opacity-only and the rendered graph was recaptured.
6. Final motion evidence: desktop and mobile show all nine filtered nodes, the selected orange route remains fully visible, seven unrelated routes are subdued, relationship evidence remains visible, and the animated edge reports `flow-money-travel` in computed browser styles.

**Primary interactions tested**

- Live profile loading and real counts.
- Upstream/global direction switching.
- Large-flow lens and node-count update.
- Relationship node selection and transaction evidence.
- Inspector collapse/reopen.
- Separate smart-investigation route with no embedded XYFlow canvas.
- Desktop and mobile browser console/page errors: none.

**Implementation Checklist**

- [x] Separate smart investigation and fund-flow routes.
- [x] Map investigation chain/address/id into the fund-flow workspace.
- [x] Use XYFlow graph interactions and motion.
- [x] Keep unsupported capabilities disabled and explained.
- [x] Verify desktop and mobile with real API data.

final result: passed

---

# Explorer Ant Design Pro Design QA

**Source visual truth**

- Query reference: `C:\Users\Euripides\.codex\generated_images\01a0bf8c-3f09-77d3-8c7f-4088d7db0cb8\exec-d42ee4e9-e523-4d03-a86e-8e6288dd49e9.png`
- Address result reference: `C:\Users\Euripides\.codex\generated_images\01a0bf8c-3f09-77d3-8c7f-4088d7db0cb8\exec-b8fca98d-c987-4528-99db-b60028c3e2b2.png`
- The two accepted mockups are the visual target. All values in production continue to come from the real Explorer APIs; fixture values exist only in visual acceptance scripts.

**Rendered implementation**

- Query desktop: `E:\Code\远程虚拟机\.codex-pro-redesign-20260923\screens\query-desktop-final.png`
- Query mobile: `E:\Code\远程虚拟机\.codex-pro-redesign-20260923\screens\query-mobile.png`
- Address desktop: `E:\Code\远程虚拟机\.codex-pro-redesign-20260923\screens\address-desktop-final.png`
- Address mobile: `E:\Code\远程虚拟机\.codex-pro-redesign-20260923\screens\address-mobile-v2.png`
- Viewports checked: 1440×900, 768×1024, 390×844, and 320×800 at device scale factor 1.

**Full-view comparison evidence**

- Background and clarity: the Explorer content canvas is true white, typography uses the existing crisp ChainSight font tokens, and no gray wash or translucent glass effect remains.
- Query hierarchy: title/meta remain in the existing shell; query controls are grouped into one outlined surface with a short blue top indicator, matching the accepted composition.
- Recent list: rows share one bordered list surface with hairline separators, chain icons, compact metadata, and right-aligned actions rather than independent floating cards.
- Address hierarchy: object header, metadata, nine-tab navigation, overview/main column, and source inspector keep the accepted left-main/right-inspector layout.
- Metrics: five values render in one StatisticCard.Group-like surface with internal hairlines, blue line icons, and no floating-card shadows.
- Balance table: columns are aligned to the accepted table — 资产, 余额, 价格 USD, 估值 USD, 更新时间 — while retaining full real values and timestamps.
- Tabs: address and transaction detail pages use Ant Tabs ink-bar behavior; all labels remain anchor deep links and keep their original URL keys.

**Focused region comparison evidence**

- The chain Select, address Input, and query Button all measure 48px high and have identical vertical coordinates on desktop and tablet.
- The Select arrow remains centered at the right edge; the selected chain uses a fixed icon slot and a single untruncated display name.
- The address detail metric group measures five equal 159px columns at 1440px; mobile reflows to two columns without page overflow.
- The source inspector remains a distinct white outlined surface; its resizer is a one-pixel neutral line with a blue hover/focus state.
- Mobile Tabs stay horizontally scrollable and preserve all nine address destinations rather than collapsing or deleting items.

**Copy/content differences**

- The implementation retains the repository's real Chinese product terms: “代币转账” and “内部交易” instead of the mockup's English labels where the existing contract already uses Chinese.
- The reference mock's sample addresses, counts, balances, providers, and timestamps were not copied into production code.
- Real empty states continue to display `—` or the existing empty-state component instead of invented values.

**Intentional deviations**

- `@ant-design/pro-components` remains at current latest 2.8.10 with compatible `antd` 5.29.3. Ant Design 6.6.5 was not installed because ProComponents 2.8.10 officially peers with Ant Design 4.24.15 or 5.11.2 and does not declare Ant 6 compatibility.
- Existing shell dimensions, permissions, API paths, Dialog behavior, localStorage key, and resizable inspector were preserved even where the static mock does not show them.
- The desktop acceptance viewport is 1440×900 rather than the mock's wider canvas, so the main table is denser while keeping the same section structure.

**Primary interactions tested**

- Chain dropdown opening, viewport containment, and Polygon selection.
- Address and transaction input routing with unchanged paths.
- Help Dialog and clear-confirm Dialog open/close behavior.
- Address Tab URL updates and all nine deep-link href values.
- Transaction Overview/Logs/Trace Tab labels and URL behavior.
- Desktop, tablet, mobile, and compact overflow checks; browser console and page errors are zero.
- Full user-console render suite, permission matrix, reduced-motion behavior, CSP, and cross-page smoke routes.

**Implementation Checklist**

- [x] White, crisp Explorer canvas without gray wash.
- [x] Query controls aligned to one 48px baseline.
- [x] Ant Tabs applied to address and transaction detail pages.
- [x] Grouped overview metrics with hairline dividers.
- [x] Current balance table aligned to the approved column design.
- [x] Real APIs, routes, permissions, localStorage, precision, and deep links preserved.
- [x] Desktop/mobile visual and interaction acceptance passed.

final result: passed
