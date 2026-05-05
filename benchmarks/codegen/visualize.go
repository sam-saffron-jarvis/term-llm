package main

import (
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func writeVisualizations(outDir string, report RunReport) error {
	if len(report.Tasks) == 0 {
		return nil
	}
	svg := renderDashboardSVG(report)
	htmlDoc := renderDashboardHTML(report, svg)
	base := fmt.Sprintf("%s_%s", report.ID, safeSlug(report.ProviderModel()))
	if err := os.WriteFile(filepath.Join(outDir, base+"_dashboard.svg"), []byte(svg), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, base+"_dashboard.html"), []byte(htmlDoc), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "latest_dashboard.svg"), []byte(svg), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "latest_dashboard.html"), []byte(htmlDoc), 0o644)
}

func (r RunReport) ProviderModel() string {
	if strings.TrimSpace(r.Model) == "" {
		return r.Provider
	}
	return r.Provider + "-" + r.Model
}

func renderDashboardHTML(report RunReport, svg string) string {
	var rows strings.Builder
	tasks := append([]TaskResult(nil), report.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Task < tasks[j].Task })
	for _, t := range tasks {
		pass := "✗"
		if t.Pass {
			pass = "✓"
		}
		perf := displayRuntime(t)
		fmt.Fprintf(&rows, `<tr><td>%s</td><td>%s</td><td class="pass">%s</td><td>%.2f</td><td>$%.4f</td><td>%s</td><td>%dms</td><td>%dms</td><td>%s</td></tr>`+"\n",
			html.EscapeString(t.Task), html.EscapeString(t.Language), pass, t.Score, t.EstimatedCost, html.EscapeString(perf), t.LLMDurationMS, t.ScoreDurationMS, html.EscapeString(firstNonEmpty(t.Details, t.Error)))
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>term-llm codegen benchmark %s</title>
<style>
  body { margin: 0; background: #f6f7fb; color: #151923; font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  main { max-width: 1180px; margin: 0 auto; padding: 28px; }
  h1 { margin: 0 0 4px; font-size: 28px; }
  .muted { color: #667085; }
  .cards { display: grid; grid-template-columns: repeat(5, 1fr); gap: 12px; margin: 20px 0; }
  .card { background: white; border: 1px solid #e4e7ec; border-radius: 16px; padding: 14px 16px; box-shadow: 0 1px 2px rgb(16 24 40 / 5%%); }
  .label { color: #667085; font-size: 12px; text-transform: uppercase; letter-spacing: .06em; }
  .value { font-size: 24px; font-weight: 760; margin-top: 4px; }
  .viz { background: white; border: 1px solid #e4e7ec; border-radius: 20px; padding: 12px; box-shadow: 0 12px 30px rgb(16 24 40 / 8%%); overflow: auto; }
  table { width: 100%%; border-collapse: collapse; margin-top: 20px; background: white; border-radius: 16px; overflow: hidden; box-shadow: 0 1px 2px rgb(16 24 40 / 5%%); }
  th, td { padding: 10px 12px; border-bottom: 1px solid #eaecf0; text-align: left; white-space: nowrap; }
  th { background: #f9fafb; color: #475467; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
  td.pass { font-size: 18px; }
  .note { margin-top: 12px; color: #667085; }
  @media (max-width: 900px) { .cards { grid-template-columns: repeat(2, 1fr); } main { padding: 16px; } }
</style>
</head>
<body>
<main>
  <h1>Codegen benchmark dashboard</h1>
  <div class="muted">%s · provider %s · concurrency %d · budget %s</div>
  <section class="cards">
    <div class="card"><div class="label">Quality</div><div class="value">%.0f%%</div></div>
    <div class="card"><div class="label">Passes</div><div class="value">%d/%d</div></div>
    <div class="card"><div class="label">Cost</div><div class="value">$%.4f</div></div>
    <div class="card"><div class="label">Cost / pass</div><div class="value">$%.4f</div></div>
    <div class="card"><div class="label">Wall time</div><div class="value">%s</div></div>
  </section>
  <section class="viz">%s</section>
  <div class="note">Bubble chart: x = generation cost, y = measured runtime speed when available, otherwise scoring speed. Green passes. Red fails. Bigger bubbles cost more. The chart intentionally punishes expensive slow failures because so should we.</div>
  <table>
    <thead><tr><th>Task</th><th>Lang</th><th>Pass</th><th>Score</th><th>Cost</th><th>Runtime</th><th>LLM</th><th>Score time</th><th>Detail</th></tr></thead>
    <tbody>%s</tbody>
  </table>
</main>
</body>
</html>`, html.EscapeString(report.ID), html.EscapeString(report.ID), html.EscapeString(report.ProviderModel()), report.Concurrency, html.EscapeString(report.Budget), report.PassRate*100, report.Passes, report.Total, report.EstimatedCostUSD, report.EstimatedCostPerPass, (time.Duration(report.TotalDurationMS) * time.Millisecond).String(), svg, rows.String())
}

func renderDashboardSVG(report RunReport) string {
	const width = 1120
	const height = 680
	const left = 82
	const top = 72
	const plotW = 650
	const plotH = 500

	tasks := append([]TaskResult(nil), report.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Task < tasks[j].Task })

	maxCost := 0.0001
	minPerf := math.MaxFloat64
	maxPerf := 0.0
	for _, t := range tasks {
		maxCost = math.Max(maxCost, t.EstimatedCost)
		p := perfForChart(t)
		minPerf = math.Min(minPerf, p)
		maxPerf = math.Max(maxPerf, p)
	}
	if minPerf == math.MaxFloat64 {
		minPerf = 0
	}
	if maxPerf <= minPerf {
		maxPerf = minPerf + 1
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" height="auto">`, width, height))
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	b.WriteString(`<defs><filter id="shadow" x="-20%" y="-20%" width="140%" height="140%"><feDropShadow dx="0" dy="8" stdDeviation="10" flood-color="#101828" flood-opacity="0.16"/></filter></defs>`)
	b.WriteString(`<text x="40" y="38" font-size="25" font-weight="800" fill="#101828">Cost × performance × quality</text>`)
	b.WriteString(fmt.Sprintf(`<text x="40" y="60" font-size="13" fill="#667085">%s · %s</text>`, esc(report.ProviderModel()), esc(report.ID)))

	// Plot panel.
	b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="18" fill="#f8fafc" stroke="#e4e7ec"/>`, left-30, top-30, plotW+60, plotH+70))
	for i := 0; i <= 4; i++ {
		x := left + i*plotW/4
		y := top + i*plotH/4
		b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#e4e7ec"/>`, x, top, x, top+plotH))
		b.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#e4e7ec"/>`, left, y, left+plotW, y))
	}
	b.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="12" fill="#667085">cheaper generation →</text>`, left+plotW-130, top+plotH+36))
	b.WriteString(fmt.Sprintf(`<text transform="translate(%d,%d) rotate(-90)" font-size="12" fill="#667085">faster runtime / scoring →</text>`, left-48, top+150))

	for _, t := range tasks {
		costRatio := 0.0
		if maxCost > 0 {
			costRatio = t.EstimatedCost / maxCost
		}
		perf := perfForChart(t)
		perfRatio := (maxPerf - perf) / (maxPerf - minPerf)
		if math.IsNaN(perfRatio) || math.IsInf(perfRatio, 0) {
			perfRatio = 0.5
		}
		x := float64(left) + costRatio*float64(plotW)
		y := float64(top+plotH) - perfRatio*float64(plotH)
		r := 8 + math.Sqrt(math.Max(t.EstimatedCost, 0)/maxCost)*18
		fill := "#12b76a"
		stroke := "#087443"
		if !t.Pass {
			fill = "#f04438"
			stroke = "#b42318"
		} else if t.Score < 1 {
			fill = "#fdb022"
			stroke = "#b54708"
		}
		b.WriteString(fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" stroke="%s" stroke-width="2" opacity="0.88" filter="url(#shadow)"><title>%s cost $%.4f runtime %s score %.2f</title></circle>`, x, y, r, fill, stroke, esc(t.Task), t.EstimatedCost, esc(displayRuntime(t)), t.Score))
		b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="11" font-weight="700" fill="#101828">%s</text>`, x+r+4, y+4, esc(shortTaskName(t.Task))))
	}

	// Right-side bar scoreboard.
	panelX := 790
	b.WriteString(fmt.Sprintf(`<rect x="%d" y="72" width="290" height="538" rx="18" fill="#101828"/>`, panelX))
	b.WriteString(fmt.Sprintf(`<text x="%d" y="105" font-size="18" font-weight="800" fill="#ffffff">Scoreboard</text>`, panelX+24))
	barY := 132
	for _, t := range tasks {
		label := shortTaskName(t.Task)
		costW := 0.0
		if maxCost > 0 {
			costW = 110 * t.EstimatedCost / maxCost
		}
		qualityW := 110 * math.Max(0, math.Min(1, t.Score))
		color := "#12b76a"
		if !t.Pass {
			color = "#f04438"
		}
		b.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="11" fill="#d0d5dd">%s</text>`, panelX+24, barY, esc(label)))
		b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="110" height="7" rx="4" fill="#344054"/><rect x="%d" y="%d" width="%.1f" height="7" rx="4" fill="%s"/>`, panelX+24, barY+8, panelX+24, barY+8, qualityW, color))
		b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="110" height="7" rx="4" fill="#344054"/><rect x="%d" y="%d" width="%.1f" height="7" rx="4" fill="#7c3aed"/>`, panelX+150, barY+8, panelX+150, barY+8, costW))
		barY += 48
	}
	b.WriteString(fmt.Sprintf(`<text x="%d" y="585" font-size="11" fill="#98a2b3">left bars: quality · right bars: relative cost</text>`, panelX+24))
	b.WriteString(`</svg>`)
	return b.String()
}

func safeSlug(s string) string {
	return strings.NewReplacer(":", "-", "/", "-", " ", "-", "_", "-").Replace(s)
}

func esc(s string) string { return html.EscapeString(s) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func displayRuntime(t TaskResult) string {
	if t.Metrics.RuntimeMS > 0 {
		return fmt.Sprintf("%.2fms", t.Metrics.RuntimeMS)
	}
	if t.Metrics.NSPerOp > 0 {
		return fmt.Sprintf("%.0fns/op", t.Metrics.NSPerOp)
	}
	if t.ScoreDurationMS > 0 {
		return fmt.Sprintf("score %dms", t.ScoreDurationMS)
	}
	return "n/a"
}

func perfForChart(t TaskResult) float64 {
	if t.Metrics.RuntimeMS > 0 {
		return t.Metrics.RuntimeMS
	}
	if t.Metrics.NSPerOp > 0 {
		return t.Metrics.NSPerOp / 1_000_000
	}
	if t.ScoreDurationMS > 0 {
		return float64(t.ScoreDurationMS)
	}
	return float64(maxInt64(t.DurationMS, 1))
}

func shortTaskName(name string) string {
	name = strings.TrimPrefix(name, "go_")
	name = strings.TrimPrefix(name, "node_")
	name = strings.ReplaceAll(name, "_", " ")
	if len(name) > 24 {
		return name[:21] + "…"
	}
	return name
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
