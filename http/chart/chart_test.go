package chart_test

// chart_test.go — geometry and contract tests for the SVG chart renderer. | chart_test.go —
// SVG 图表渲染器的几何与契约测试。
//
// These tests are deliberately structural rather than pixel-perfect: they pin the promises a page
// depends on (a finite document, one mark per value, semantic classes instead of colours, a title
// on every mark) without freezing the exact coordinates of a chart that is expected to evolve.
// 这些测试刻意只做结构断言而非逐像素比对：它们钉住页面真正依赖的承诺（文档不含非有限数、每个值
// 一个图形、用语义类而不是颜色、每个图形都有 title），而不冻结一张本来就会演进的图表的精确坐标。

import (
	"math"
	"strings"
	"testing"

	"github.com/shibingli/yd/http/chart"
)

// sampleSeries returns two deterministic series with four points each. | sampleSeries 返回两组各四个
// 点的确定性序列。
func sampleSeries() []chart.Series {
	return []chart.Series{
		{Label: "Requests", Points: []chart.Point{{Label: "Jan", Value: 10}, {Label: "Feb", Value: 20}, {Label: "Mar", Value: 15}, {Label: "Apr", Value: 30}}},
		{Label: "Errors", Points: []chart.Point{{Label: "Jan", Value: 2}, {Label: "Feb", Value: 4}, {Label: "Mar", Value: 1}, {Label: "Apr", Value: 3}}},
	}
}

// TestLineKeepsTheContract checks the line chart's promises. | TestLineKeepsTheContract 校验折线图的承诺。
func TestLineKeepsTheContract(t *testing.T) {
	out := chart.Line(sampleSeries(), chart.Options{Title: "Traffic", Description: "Requests and errors"})
	for _, want := range []string{`<svg`, `yd-chart--line`, `yd-chart__line`, `yd-chart__point`, `<title>Jan: 10</title>`, `<title>Apr: 30</title>`} {
		if !strings.Contains(out, want) {
			t.Errorf("line chart lacks %q | 折线图缺少 %q", want, want)
		}
	}
	for _, bad := range []string{"NaN", "Inf", "#", "rgb("} {
		if strings.Contains(out, bad) {
			t.Errorf("line chart contains %q, which must never reach the document | 折线图包含 %q，它绝不该进入文档", bad, bad)
		}
	}
	if again := chart.Line(sampleSeries(), chart.Options{Title: "Traffic", Description: "Requests and errors"}); again != out {
		t.Error("the same input produced a different document | 相同输入产生了不同的文档")
	}
}

// TestEmptyDataNeverProducesInvalidGeometry checks a degenerate series. | TestEmptyDataNeverProducesInvalidGeometry
// 校验退化序列。
func TestEmptyDataNeverProducesInvalidGeometry(t *testing.T) {
	out := chart.Line(nil, chart.Options{Title: "Empty", EmptyText: "No data"})
	if strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
		t.Errorf("empty chart contains a non-finite value: %s | 空图包含非有限数：%s", out, out)
	}
	if !strings.Contains(out, "No data") {
		t.Errorf("empty chart does not render EmptyText: %s | 空图没有渲染 EmptyText：%s", out, out)
	}
}

// TestBarAndStackedBarRenderOneMarkPerValue checks the mark counts. | TestBarAndStackedBarRenderOneMarkPerValue
// 校验图形数量。
func TestBarAndStackedBarRenderOneMarkPerValue(t *testing.T) {
	series := sampleSeries()
	bar := chart.Bar(series, chart.Options{Title: "Bars"})
	if got := strings.Count(bar, `class="yd-chart__bar`); got != 8 {
		t.Errorf("bar chart has %[1]d marks, want 8 | 柱状图有 %[1]d 个图形，期望 8", got)
	}
	stacked := chart.StackedBar(series, chart.Options{Title: "Stacked"})
	if got := strings.Count(stacked, `class="yd-chart__bar`); got != 8 {
		t.Errorf("stacked bar chart has %[1]d marks, want 8 | 堆叠柱状图有 %[1]d 个图形，期望 8", got)
	}
	if !strings.Contains(stacked, "Requests") || !strings.Contains(stacked, "Errors") {
		t.Error("stacked bars dropped a series label | 堆叠柱丢失了序列标签")
	}
}

// TestPieAndDonutRenderEverySlice checks slices and the donut hole. | TestPieAndDonutRenderEverySlice 校验
// 扇区与环形内孔。
func TestPieAndDonutRenderEverySlice(t *testing.T) {
	points := []chart.Point{{Label: "A", Value: 30}, {Label: "B", Value: 20}, {Label: "C", Value: 50}}
	pie := chart.Pie(points, chart.Options{Title: "Pie"})
	if got := strings.Count(pie, `class="yd-chart__slice`); got != 3 {
		t.Errorf("pie has %[1]d slices, want 3 | 饼图有 %[1]d 个扇区，期望 3", got)
	}
	donut := chart.Donut(points, chart.Options{Title: "Donut"})
	if got := strings.Count(donut, `class="yd-chart__slice`); got != 3 {
		t.Errorf("donut has %[1]d slices, want 3 | 环形图有 %[1]d 个扇区，期望 3", got)
	}
	if !strings.Contains(donut, " yd-chart--donut") {
		t.Error("Donut did not set the donut class | Donut 没有设置环形图类")
	}
	full := chart.Donut([]chart.Point{{Label: "All", Value: 100}}, chart.Options{Title: "Full"})
	if strings.Count(full, " A ") < 4 {
		t.Errorf("a full-circle donut needs outer and inner arcs: %s | 整圆环形图需要内外两段弧：%s", full, full)
	}
}

// TestGaugeRadarComposedAndHeatmap checks the remaining chart families. | TestGaugeRadarComposedAndHeatmap
// 校验其余图表族。
func TestGaugeRadarComposedAndHeatmap(t *testing.T) {
	gauge := chart.Gauge(72, 100, chart.Options{Title: "CPU", ShowAxis: true})
	for _, want := range []string{"yd-chart__gauge-track", "yd-chart__gauge-value", "stroke-dasharray", "<title>72</title>"} {
		if !strings.Contains(gauge, want) {
			t.Errorf("gauge lacks %q | 仪表盘缺少 %q", want, want)
		}
	}

	radar := chart.Radar([]string{"CPU", "Memory", "Disk"}, sampleSeries(), chart.Options{Title: "Radar"})
	if got := strings.Count(radar, `class="yd-chart__radar`); got != 2 {
		t.Errorf("radar has %[1]d series polygons, want 2 | 雷达图有 %[1]d 个序列多边形，期望 2", got)
	}
	if !strings.Contains(radar, "polygon") {
		t.Error("radar lacks its polygon | 雷达图缺少多边形")
	}

	composed := chart.Composed(sampleSeries()[:1], sampleSeries()[1:], chart.Options{Title: "Composed"})
	if !strings.Contains(composed, "yd-chart__bar") || !strings.Contains(composed, "yd-chart__line") {
		t.Error("composed chart must carry both bars and lines | 组合图必须同时包含柱与线")
	}

	heat := chart.Heatmap([][]float64{{1, 2, 3}, {4, 5, 6}}, []string{"A", "B", "C"}, []string{"R1", "R2"}, chart.Options{Title: "Heat"})
	if got := strings.Count(heat, `class="yd-chart__cell`); got != 6 {
		t.Errorf("heatmap has %[1]d cells, want 6 | 热力图有 %[1]d 个格子，期望 6", got)
	}
	if strings.Contains(heat, "NaN") {
		t.Error("heatmap contains NaN | 热力图包含 NaN")
	}
}

// TestProgressRingAndLegend checks the progress ring and legend helper. | TestProgressRingAndLegend 校验
// 进度环与图例辅助。
func TestProgressRingAndLegend(t *testing.T) {
	ring := chart.ProgressRing(45, 100, chart.Options{Title: "Progress"})
	if got := strings.Count(ring, "<circle"); got != 2 {
		t.Errorf("progress ring has %[1]d circles, want 2 | 进度环有 %[1]d 个圆，期望 2", got)
	}
	if !strings.Contains(ring, "stroke-dasharray") {
		t.Error("progress ring lacks its dash array | 进度环缺少 dash array")
	}
	legend := chart.Legend(sampleSeries())
	if len(legend) != 2 || legend[0].Label != "Requests" || legend[0].Class != "yd-chart__series--1" {
		t.Errorf("legend = %+v | 图例 = %+v", legend, legend)
	}
	slices := chart.SliceLegend([]chart.Point{{Label: "A"}, {Label: "B"}, {Label: "C"}})
	if len(slices) != 3 || slices[2].Class != "yd-chart__series--3" {
		t.Errorf("slice legend = %+v | 扇区图例 = %+v", slices, slices)
	}
}

// TestNoColourLiterals keeps the renderer theme-driven. | TestNoColourLiterals 保证渲染器由主题驱动。
func TestNoColourLiterals(t *testing.T) {
	outputs := []string{
		chart.Line(sampleSeries(), chart.Options{}),
		chart.Area(sampleSeries(), chart.Options{}),
		chart.Bar(sampleSeries(), chart.Options{}),
		chart.Pie([]chart.Point{{Label: "A", Value: 1}}, chart.Options{}),
		chart.Gauge(1, 2, chart.Options{}),
		chart.Radar([]string{"A", "B", "C"}, sampleSeries()[:1], chart.Options{}),
		chart.Heatmap([][]float64{{1}}, nil, nil, chart.Options{}),
		chart.ProgressRing(1, 2, chart.Options{}),
	}
	for i, out := range outputs {
		for _, literal := range []string{"#", "rgb(", "hsl(", "fill=\"color"} {
			if strings.Contains(out, literal) {
				t.Errorf("chart %d contains a colour literal %q | 图表 %d 包含颜色字面量 %q", i, literal, i, literal)
			}
		}
	}
}

// TestOptionsAreBounded checks that caller-supplied sizes and tick counts cannot turn a chart into
// an unbounded loop or an absurd viewBox. The zero value stays usable; the extremes are clamped.
// TestOptionsAreBounded 校验调用方给出的尺寸与刻度数不会把图表变成无界循环或荒唐的 viewBox。
// 零值保持可用，极端值被收敛。
func TestOptionsAreBounded(t *testing.T) {
	out := chart.Line(sampleSeries(), chart.Options{Width: 99999, Height: 99999, YTicks: 1000000, ShowGrid: true})
	if !strings.Contains(out, `viewBox="0 0 8192 8192"`) {
		t.Error("an oversized viewBox was not clamped | 过大的 viewBox 没有被收敛")
	}
	if got := strings.Count(out, `class="yd-chart__grid"`); got != 13 {
		t.Errorf("grid lines = %d, want 13 (12 ticks + upper bound) | 网格线 = %d，期望 13", got, got)
	}
}

// TestAxisLabelsDoNotRequireGrid keeps ShowAxis independent of ShowGrid: an operator may want tick
// text without rules, and the old implementation silently dropped every label in that case.
// TestAxisLabelsDoNotRequireGrid 保证 ShowAxis 与 ShowGrid 相互独立：操作员可能只想要刻度文字而
// 不想要网格线，旧实现会在这种组合下悄悄丢掉全部标签。
func TestAxisLabelsDoNotRequireGrid(t *testing.T) {
	out := chart.Line(sampleSeries(), chart.Options{Title: "Traffic", ShowGrid: false, ShowAxis: true})
	if strings.Contains(out, `class="yd-chart__grid"`) {
		t.Error("ShowGrid=false still rendered grid lines | ShowGrid=false 仍然渲染了网格线")
	}
	if !strings.Contains(out, `yd-chart__axis-text`) {
		t.Error("ShowAxis=true rendered no axis labels | ShowAxis=true 没有渲染刻度标签")
	}
}

// TestStackedBarRejectsNegativeValues prevents every following slice from being drawn from the wrong
// baseline. | TestStackedBarRejectsNegativeValues 防止后续每一片都从错误基线开始绘制。
func TestStackedBarRejectsNegativeValues(t *testing.T) {
	out := chart.StackedBar([]chart.Series{
		{Label: "A", Points: []chart.Point{{Label: "Jan", Value: 10}}},
		{Label: "B", Points: []chart.Point{{Label: "Jan", Value: -3}}},
	}, chart.Options{Title: "Stacked", EmptyText: "No data"})
	if !strings.Contains(out, `yd-chart__empty`) || strings.Contains(out, `yd-chart__bar`) {
		t.Error("a negative stacked value must render the empty state, not a misleading bar | 负值堆叠必须渲染空状态，而不是误导性的柱子")
	}
}

// TestGaugeAndRingRejectNonFiniteMax keeps NaN/Inf out of the geometry. | TestGaugeAndRingRejectNonFiniteMax
// 保证 NaN/Inf 不进入几何。
func TestGaugeAndRingRejectNonFiniteMax(t *testing.T) {
	nanMax := math.NaN()
	for name, out := range map[string]string{
		"gauge": chart.Gauge(1, nanMax, chart.Options{Title: "Gauge", EmptyText: "No data"}),
		"ring":  chart.ProgressRing(1, nanMax, chart.Options{Title: "Ring", EmptyText: "No data"}),
	} {
		if !strings.Contains(out, "yd-chart__empty") {
			t.Errorf("%s with NaN max rendered geometry instead of the empty state | %s 的 max 为 NaN 时渲染了几何而不是空状态", name, name)
		}
		for _, bad := range []string{"NaN", "Inf"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s contains %s | %s 包含 %s", name, bad, name, bad)
			}
		}
	}
}
