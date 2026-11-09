package statuspage

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

type pageDay struct {
	Date, State string
}

type pageComponent struct {
	Name, Description, Group, State, StateLabel, Summary, Uptime, Counts, LatencyLabel string
	ShowGroup                                                                          bool
	LatencyMS                                                                          int
	Days                                                                               []pageDay
	Spark                                                                              template.HTML
	ShowSpark                                                                          bool
}

type pageLog struct {
	Clock, Component, State, StateLabel, Summary string
	LatencyMS                                    int
}

type pageModel struct {
	Overall, Summary, OverallLabel, Generated string
	Groups                                    []string
	Components                                []pageComponent
	Logs                                      []pageLog
}

var pageTmpl = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="30">
  <title>Rida status</title>
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body { margin: 0; background: #f6f5f2; color: #1c1b19; font: 15px/1.45 ui-sans-serif, system-ui, sans-serif; }
    main { max-width: 980px; margin: 0 auto; padding: 28px 20px 64px; }
    header { display: flex; justify-content: space-between; gap: 16px; align-items: baseline; margin-bottom: 8px; }
    h1 { font-size: 22px; margin: 0; letter-spacing: -0.02em; }
    .range { color: #8a8478; font-size: 13px; }
    .banner { display: flex; align-items: center; gap: 10px; margin: 18px 0 28px; font-size: 28px; font-weight: 650; letter-spacing: -0.03em; }
    .dot { width: 12px; height: 12px; border-radius: 99px; background: #3dba5d; }
    .banner.outage .dot { background: #e5534b; }
    .banner.degraded .dot { background: #f0b429; }
    h2 { margin: 28px 0 8px; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: #8a8478; font-weight: 650; }
    article { padding: 18px 0 16px; border-top: 1px solid #e6e2da; }
    .row { display: flex; justify-content: space-between; gap: 12px; align-items: baseline; }
    .name { font-weight: 650; }
    .desc { color: #6f6a62; font-size: 13px; margin-top: 2px; }
    .state { font-weight: 650; color: #2f9e44; }
    .state.degraded { color: #b58105; }
    .state.outage { color: #d23b34; }
    .state.maintenance, .state.none { color: #6f6a62; }
    .bars { display: flex; gap: 3px; height: 34px; margin-top: 12px; }
    .bar { flex: 1 1 0; min-width: 2px; border-radius: 2px; background: #e4e1da; }
    .bar.operational { background: #3dba5d; }
    .bar.degraded { background: #f0b429; }
    .bar.outage { background: #e5534b; }
    .bar.maintenance { background: #9aa0a6; }
    .axis { display: flex; align-items: center; gap: 10px; margin-top: 8px; color: #8a8478; font-size: 12px; }
    .axis i { flex: 1; height: 1px; background: #ddd9d0; }
    .axis b { color: #5f5a53; font-weight: 600; }
    .meta { display: flex; justify-content: space-between; gap: 12px; align-items: center; margin-top: 8px; color: #8a8478; font-size: 12px; }
    .spark { width: 148px; height: 28px; }
    .legend { display: flex; flex-wrap: wrap; gap: 14px; color: #6f6a62; font-size: 12px; margin: 8px 0 0; }
    .legend span::before { content: ""; display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: 6px; vertical-align: -1px; background: #3dba5d; }
    .legend .degraded::before { background: #f0b429; }
    .legend .outage::before { background: #e5534b; }
    .legend .maintenance::before { background: #9aa0a6; }
    .legend .none::before { background: #e4e1da; }
    .logs { list-style: none; margin: 8px 0 0; padding: 0; max-height: 360px; overflow: auto; border: 1px solid #e6e2da; border-radius: 12px; background: #fff; }
    .logs li { display: grid; grid-template-columns: 92px minmax(120px, 180px) 110px 1fr auto; gap: 10px; padding: 8px 12px; border-top: 1px solid #f0ece6; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
    .logs li:first-child { border-top: 0; }
    footer { margin-top: 36px; color: #8a8478; font-size: 12px; }
    @media (max-width: 720px) {
      .banner { font-size: 22px; }
      .logs li { grid-template-columns: 1fr; gap: 2px; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <h1>Rida status</h1>
      <p class="range">Uptime over the past 90 days</p>
    </header>
    <p class="banner {{.Overall}}"><i class="dot"></i>{{.Summary}}</p>
    <p class="legend"><span>Operational</span><span class="degraded">Degraded</span><span class="outage">Outage</span><span class="maintenance">Maintenance</span><span class="none">No data</span></p>
    {{range .Components}}
      {{if .ShowGroup}}<h2>{{.Group}}</h2>{{end}}
      <article>
        <div class="row">
          <div>
            <div class="name">{{.Name}}</div>
            <div class="desc">{{.Summary}}</div>
          </div>
          <div class="state {{.State}}">{{.StateLabel}}</div>
        </div>
        <div class="bars" role="img" aria-label="{{.Name}} uptime history">
          {{range .Days}}<i class="bar {{.State}}" title="{{.Date}} {{.State}}"></i>{{end}}
        </div>
        <div class="axis"><span>90 days ago</span><i></i><b>{{.Uptime}}</b><i></i><span>Today</span></div>
        <div class="meta">
          <span>{{.Counts}}</span>
          {{if .ShowSpark}}<span>{{.LatencyLabel}}{{if .LatencyMS}} · {{.LatencyMS}} ms{{end}}</span>{{.Spark}}{{end}}
        </div>
      </article>
    {{end}}
    <footer>Refreshed {{.Generated}} EAT.</footer>
  </main>
</body>
</html>
`))

func renderPage(report PublicReport) ([]byte, error) {
	model := pageModel{
		Overall: report.Overall, Summary: report.Summary, OverallLabel: stateLabel(report.Overall),
		Generated: generatedClock(report.GeneratedAt),
	}
	var previous string
	for _, component := range report.Components {
		item := pageComponent{
			Name: component.Name, Group: component.Group,
			State: component.State, StateLabel: stateLabel(component.State), Summary: component.Summary,
			Uptime: uptimeLabel(component.UptimePercent, component.State), Counts: historyCounts(component.Days),
			ShowGroup: component.Group != previous,
		}
		previous = component.Group
		for _, day := range component.Days {
			item.Days = append(item.Days, pageDay{Date: day.Date, State: day.State})
		}
		model.Components = append(model.Components, item)
	}
	var buf strings.Builder
	if err := pageTmpl.Execute(&buf, model); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func stateLabel(state string) string {
	switch state {
	case "degraded":
		return "Degraded"
	case "outage":
		return "Outage"
	case "maintenance":
		return "Maintenance"
	case "none":
		return "No data"
	default:
		return "Operational"
	}
}

func uptimeLabel(pct *float64, state string) string {
	if pct == nil {
		if state == string(Maintenance) {
			return "Not counted"
		}
		return "Collecting uptime"
	}
	return fmt.Sprintf("%.2f%% uptime", *pct)
}

func historyCounts(days []DayView) string {
	var operational, degraded, outage, maintenance int
	for _, day := range days {
		switch day.State {
		case "operational":
			operational++
		case "degraded":
			degraded++
		case "outage":
			outage++
		case "maintenance":
			maintenance++
		}
	}
	var parts []string
	if outage > 0 {
		parts = append(parts, fmt.Sprintf("%d days with an outage", outage))
	}
	if degraded > 0 {
		parts = append(parts, fmt.Sprintf("%d degraded days", degraded))
	}
	if maintenance > 0 {
		parts = append(parts, fmt.Sprintf("%d maintenance days", maintenance))
	}
	if len(parts) == 0 {
		if operational == 0 {
			return "History fills in while this process is running."
		}
		return fmt.Sprintf("%d operational days in this window.", operational)
	}
	return strings.Join(parts, " · ")
}

func sparkSVG(points []LatencyPoint) template.HTML {
	max := 1
	for _, point := range points {
		if point.MS > max {
			max = point.MS
		}
	}
	coords := make([]string, len(points))
	for i, point := range points {
		x := float64(i) / float64(len(points)-1) * 148
		y := 28 - (float64(point.MS)/float64(max))*26 - 1
		coords[i] = fmt.Sprintf("%.2f,%.2f", x, y)
	}
	return template.HTML(`<svg class="spark" viewBox="0 0 148 28" aria-hidden="true"><polyline fill="none" stroke="#1C7C70" stroke-width="1.5" points="` + strings.Join(coords, " ") + `"/></svg>`)
}

func generatedClock(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return parsed.In(eat).Format("02 Jan 15:04")
}

func logClock(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return parsed.In(eat).Format("15:04:05")
}
