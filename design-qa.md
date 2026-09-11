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
