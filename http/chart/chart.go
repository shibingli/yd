// Package chart renders dependency-free SVG charts for yd pages.
// Package chart 为 yd 页面渲染无依赖的 SVG 图表。
//
// Why server-rendered SVG | 为什么由服务端渲染 SVG
//
//	The pages already receive their data on the server through htmx fragments. Rendering the chart
//	there keeps one source of truth: no JSON payload for a client library to re-parse, no second
//	copy of the series in JavaScript, and no chart bundle in the binary. The output is a plain
//	<svg> that inherits the page's semantic colour tokens through CSS classes, so the light/dark
//	switch reaches every series without a second theme table.
//	页面本来就通过 htmx 片段在服务端取数。图表也在服务端渲染，事实来源因此只有一个：不需要为
//	客户端图表库再准备一份 JSON，不在 JavaScript 里复制第二份序列，也不把图表库打进二进制。
//	产物是普通 <svg>，经 CSS 类继承页面的语义颜色令牌，明暗切换因此不需要第二套主题表。
//
// Hard rules | 硬规则
//
//  1. No colour literals: every shape carries a class, and the colour comes from ui.css tokens.
//     不得出现颜色字面量：每个图形都带类名，颜色来自 ui.css 的令牌。
//  2. No NaN / Inf in the document: a degenerate series is replaced by an empty plot, never by
//     invalid geometry that makes the browser drop the whole SVG.
//     文档中不得出现 NaN / Inf：退化序列渲染为空图，而不是产生让浏览器丢掉整个 SVG 的非法几何。
//  3. Every mark carries a <title>, so a hover tooltip and a screen reader both get the value
//     without a JavaScript tooltip layer.
//     每个图形都带 <title>，悬停提示与读屏都能读到数值，不需要 JavaScript 提示层。
//  4. The output is deterministic for the same input: tests compare the exact document.
//     相同输入的输出确定：测试可以直接比对文档。
package chart

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// Point is one value on a series. Label is the x-axis label. | Point 是序列上的一个值，Label 是 x 轴标签。
type Point struct {
	// Label is shown on the x axis and inside the mark's <title>. | Label 显示在 x 轴与图形的 <title> 中。
	Label string
	// Value is the numeric value. | Value 是数值。
	Value float64
}

// Series is one line, area or bar series. | Series 是一条折线、面积或柱状序列。
type Series struct {
	// Label is the legend text. | Label 是图例文字。
	Label string
	// Class overrides the automatic series colour class (yd-chart__series--N). | Class 覆盖自动系列颜色类。
	Class string
	// Points are the values in x order. | Points 是按 x 顺序排列的取值。
	Points []Point
}

// LegendItem is one legend entry returned alongside the SVG. | LegendItem 是随 SVG 一起返回的图例项。
type LegendItem struct {
	// Label is the series or slice label. | Label 是序列或扇区标签。
	Label string
	// Class is the CSS class carrying the series colour. | Class 是承载系列颜色的 CSS 类。
	Class string
}

// Options controls the frame of a chart. The zero value is usable. | Options 控制图表的画框，零值即可用。
type Options struct {
	// ValueFormat formats tick labels and titles; nil means a compact decimal. | ValueFormat 格式化
	// 刻度与标题；nil 表示紧凑小数。
	ValueFormat func(float64) string
	// Title and Description become the SVG <title> and <desc>. | Title 与 Description 进入 SVG 的
	// <title> 与 <desc>。
	Title, Description string
	// Class is appended to the root svg class. | Class 追加到根 svg 的类名。
	Class string
	// EmptyText is rendered when there is no finite data; empty means no text. | 没有有效数据时渲染的
	// 文案；为空表示不渲染文字。
	EmptyText string
	// Width and Height are the viewBox dimensions; CSS scales the SVG fluidly. | Width 与 Height 是
	// viewBox 尺寸；CSS 负责流式缩放。
	Width, Height int
	// YMin and YMax pin the value axis; when YMax <= YMin the range is derived from the data. |
	// YMin 与 YMax 固定数值轴；YMax <= YMin 时由数据推导范围。
	YMin, YMax float64
	// YTicks is the number of value grid lines; 0 means four. | YTicks 是数值网格线条数，0 表示四条。
	YTicks int
	// InnerRadius is the donut hole ratio (0–1) used by Pie; 0 draws a full pie. | InnerRadius 是
	// 环形图内孔比例（0–1）；0 表示实心饼图。
	InnerRadius float64
	// ShowGrid and ShowAxis control the background grid and the axis labels. | ShowGrid 与 ShowAxis
	// 控制背景网格与轴标签。
	ShowGrid, ShowAxis bool
}

// defaultWidth and defaultHeight are the viewBox dimensions used when Options leaves them zero.
// defaultWidth 与 defaultHeight 是 Options 留空时使用的 viewBox 尺寸。
const (
	defaultWidth  = 640
	defaultHeight = 320
	// plotLeft, plotRight, plotTop and plotBottom are the frame margins in viewBox units. | 画框留白，
	// 单位为 viewBox 坐标。
	plotLeft   = 52.0
	plotRight  = 18.0
	plotTop    = 18.0
	plotBottom = 38.0
	// maxXTicks bounds the number of x labels so a long series stays readable. | maxXTicks 限制 x 轴
	// 标签数量，使长序列仍然可读。
	maxXTicks = 8
	// seriesClasses is the number of automatic series colour classes in ui.css. | seriesClasses 是
	// ui.css 中自动系列颜色类的数量。
	seriesClasses = 8
)

// normalize fills the defaults and sanitises the range. | normalize 填充默认值并规范化取值范围。
func (o Options) normalize() Options {
	if o.Width <= 0 {
		o.Width = defaultWidth
	}
	if o.Height <= 0 {
		o.Height = defaultHeight
	}
	// The viewBox is not a place for unbounded input: an 8192-unit canvas is already larger than
	// any dashboard, and a million grid lines would be a denial of service with a pretty picture.
	// viewBox 不是放置无界输入的地方：8192 单位已经大于任何仪表盘，而一百万条网格线会变成
	// 一张漂亮的拒绝服务图。
	if o.Width > 8192 {
		o.Width = 8192
	}
	if o.Height > 8192 {
		o.Height = 8192
	}
	if o.YTicks <= 0 {
		o.YTicks = 4
	}
	if o.YTicks > 12 {
		o.YTicks = 12
	}
	if o.InnerRadius < 0 {
		o.InnerRadius = 0
	}
	if o.InnerRadius > 0.9 {
		o.InnerRadius = 0.9
	}
	if o.ValueFormat == nil {
		o.ValueFormat = formatValue
	}
	return o
}

// formatValue renders a number without a trailing ".0". | formatValue 渲染数字，且不保留多余的 ".0"。
func formatValue(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// finite reports whether every point of the series is a finite number. | finite 报告序列的每个点是否为有限数。
func finite(series []Series) bool {
	for _, s := range series {
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				return false
			}
		}
	}
	return true
}

// bounds returns the value range, deriving it from the data when the caller did not pin it. | bounds
// 返回取值区间；调用方未固定时由数据推导。
func bounds(series []Series, o Options) (float64, float64) {
	if o.YMax > o.YMin {
		return o.YMin, o.YMax
	}
	min, max := 0.0, 0.0
	seen := false
	for _, s := range series {
		for _, p := range s.Points {
			if !seen {
				min, max, seen = p.Value, p.Value, true
				continue
			}
			if p.Value < min {
				min = p.Value
			}
			if p.Value > max {
				max = p.Value
			}
		}
	}
	if !seen {
		return 0, 1
	}
	if min > 0 {
		min = 0
	}
	if max < 0 {
		max = 0
	}
	if max == min {
		max = min + 1
	}
	return min, max
}

// seriesClass returns the CSS class of the i-th series. | seriesClass 返回第 i 条序列的 CSS 类。
func seriesClass(s Series, i int) string {
	if strings.TrimSpace(s.Class) != "" {
		return s.Class
	}
	return fmt.Sprintf("yd-chart__series--%d", i%seriesClasses+1)
}

// escape escapes text for XML/SVG. | escape 转义 XML/SVG 文本。
func escape(s string) string { return html.EscapeString(s) }

// number formats a coordinate with at most two decimals. | number 以至多两位小数格式化坐标。
func number(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// svg is the small writer shared by every chart. | svg 是全部图表共用的小型写入器。
type svg struct {
	b      strings.Builder
	o      Options
	width  int
	height int
	left   float64
	right  float64
	top    float64
	bottom float64
}

// newSVG opens an SVG document and returns the writer. | newSVG 打开 SVG 文档并返回写入器。
func newSVG(o Options, kind string) *svg {
	o = o.normalize()
	s := &svg{
		width:  o.Width,
		height: o.Height,
		left:   plotLeft,
		right:  float64(o.Width) - plotRight,
		top:    plotTop,
		bottom: float64(o.Height) - plotBottom,
		o:      o,
	}
	class := "yd-chart yd-chart--" + kind
	if strings.TrimSpace(o.Class) != "" {
		class += " " + strings.TrimSpace(o.Class)
	}
	fmt.Fprintf(&s.b, `<svg class="%s" viewBox="0 0 %d %d" role="img" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">`, escape(class), o.Width, o.Height)
	if o.Title == "" {
		// The package has no locale context; this bilingual fallback is the one neutral name that
		// never claims to be a translated page title. Callers pass Options.Title from tr().
		// 本包没有语言上下文；这个双语兜底是唯一中立的名称，不会冒充已翻译的页面标题。
		// 调用方应通过 tr() 填入 Options.Title。
		o.Title = "Chart | 图表"
	}
	fmt.Fprintf(&s.b, `<title>%s</title>`, escape(o.Title))
	if o.Description != "" {
		fmt.Fprintf(&s.b, `<desc>%s</desc>`, escape(o.Description))
	}
	return s
}

// close closes the SVG document. | close 关闭 SVG 文档。
func (s *svg) close() string {
	s.b.WriteString(`</svg>`)
	return s.b.String()
}

// plotWidth and plotHeight return the inner plot size. | plotWidth 与 plotHeight 返回绘图区尺寸。
func (s *svg) plotWidth() float64  { return s.right - s.left }
func (s *svg) plotHeight() float64 { return s.bottom - s.top }

// y returns the pixel coordinate of a value. | y 返回数值的像素坐标。
func (s *svg) y(v, min, max float64) float64 {
	if max <= min {
		return s.bottom
	}
	ratio := (v - min) / (max - min)
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return s.bottom
	}
	return s.bottom - ratio*s.plotHeight()
}

// x returns the pixel coordinate of the i-th point out of n. | x 返回 n 个点中第 i 个的像素坐标。
func (s *svg) x(i, n int) float64 {
	if n <= 1 {
		return s.left + s.plotWidth()/2
	}
	return s.left + float64(i)/float64(n-1)*s.plotWidth()
}

// text writes one SVG text node. | text 写入一个 SVG 文本节点。
func (s *svg) text(x, y float64, class, body string) {
	fmt.Fprintf(&s.b, `<text class="%s" x="%s" y="%s">%s</text>`, escape(class), number(x), number(y), escape(body))
}

// frame draws the grid and the axis labels shared by cartesian charts. | frame 绘制直角坐标图表共用的网格与轴标签。
func (s *svg) frame(min, max float64, firstLabels []string, count int) {
	if s.o.ShowGrid {
		for i := 0; i <= s.o.YTicks; i++ {
			ratio := float64(i) / float64(s.o.YTicks)
			y := s.bottom - ratio*s.plotHeight()
			fmt.Fprintf(&s.b, `<line class="yd-chart__grid" x1="%s" y1="%s" x2="%s" y2="%s"/>`, number(s.left), number(y), number(s.right), number(y))
		}
	}
	if s.o.ShowAxis {
		// Value labels are independent of the grid: a caller may want tick text without rules.
		// 数值标签与网格相互独立：调用方可能只想要刻度文字而不想要网格线。
		for i := 0; i <= s.o.YTicks; i++ {
			ratio := float64(i) / float64(s.o.YTicks)
			y := s.bottom - ratio*s.plotHeight()
			s.text(s.left-8, y+4, "yd-chart__axis-text", s.o.ValueFormat(min+(max-min)*ratio))
		}
	}
	if s.o.ShowAxis && len(firstLabels) > 0 {
		step := 1
		if len(firstLabels) > maxXTicks {
			step = int(math.Ceil(float64(len(firstLabels)) / maxXTicks))
		}
		for i, label := range firstLabels {
			if i%step != 0 && i != len(firstLabels)-1 {
				continue
			}
			x := s.x(i, count)
			s.text(x, s.bottom+20, "yd-chart__axis-text yd-chart__axis-text--x", label)
		}
	}
}

// linePath builds the polyline path of one series. | linePath 构建一条序列的折线路径。
func (s *svg) linePath(points []Point, min, max float64) string {
	var b strings.Builder
	for i, p := range points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&b, "%s%s %s ", cmd, number(s.x(i, len(points))), number(s.y(p.Value, min, max)))
	}
	return strings.TrimSpace(b.String())
}

// Line renders a line chart. | Line 渲染折线图。
func Line(series []Series, o Options) string {
	if !finite(series) || len(series) == 0 {
		return empty(o, "line")
	}
	o = o.normalize()
	min, max := bounds(series, o)
	s := newSVG(o, "line")
	s.frame(min, max, labelsOf(series[0]), len(series[0].Points))
	for i, ser := range series {
		class := seriesClass(ser, i)
		fmt.Fprintf(&s.b, `<g class="yd-chart__series %s">`, escape(class))
		fmt.Fprintf(&s.b, `<path class="yd-chart__line" d="%s"/>`, s.linePath(ser.Points, min, max))
		for j, p := range ser.Points {
			fmt.Fprintf(&s.b, `<circle class="yd-chart__point" cx="%s" cy="%s" r="3"><title>%s: %s</title></circle>`,
				number(s.x(j, len(ser.Points))), number(s.y(p.Value, min, max)), escape(p.Label), escape(o.ValueFormat(p.Value)))
		}
		s.b.WriteString(`</g>`)
	}
	return s.close()
}

// Area renders an area chart with a translucent fill under the line. | Area 渲染带半透明填充的面积图。
func Area(series []Series, o Options) string {
	if !finite(series) || len(series) == 0 {
		return empty(o, "area")
	}
	o = o.normalize()
	min, max := bounds(series, o)
	s := newSVG(o, "area")
	s.frame(min, max, labelsOf(series[0]), len(series[0].Points))
	for i, ser := range series {
		class := seriesClass(ser, i)
		fmt.Fprintf(&s.b, `<g class="yd-chart__series %s">`, escape(class))
		path := s.linePath(ser.Points, min, max)
		if path != "" {
			fmt.Fprintf(&s.b, `<path class="yd-chart__area" d="%s L%s %s L%s %s Z"/>`, path,
				number(s.right), number(s.bottom), number(s.left), number(s.bottom))
		}
		fmt.Fprintf(&s.b, `<path class="yd-chart__line" d="%s"/>`, path)
		s.b.WriteString(`</g>`)
	}
	return s.close()
}

// Bar renders a grouped bar chart. | Bar 渲染分组柱状图。
func Bar(series []Series, o Options) string {
	if !finite(series) || len(series) == 0 {
		return empty(o, "bar")
	}
	o = o.normalize()
	min, max := bounds(series, o)
	s := newSVG(o, "bar")
	labels := labelsOf(series[0])
	s.frame(min, max, labels, len(labels))
	count := len(labels)
	if count == 0 {
		return s.close()
	}
	groupWidth := s.plotWidth() / float64(count)
	barWidth := groupWidth * 0.7 / float64(len(series))
	zero := s.y(0, min, max)
	for i, ser := range series {
		class := seriesClass(ser, i)
		fmt.Fprintf(&s.b, `<g class="yd-chart__series %s">`, escape(class))
		for j, p := range ser.Points {
			if j >= count {
				break
			}
			x := s.left + float64(j)*groupWidth + groupWidth*0.15 + float64(i)*barWidth
			y := s.y(p.Value, min, max)
			top := math.Min(y, zero)
			height := math.Abs(zero - y)
			fmt.Fprintf(&s.b, `<rect class="yd-chart__bar" x="%s" y="%s" width="%s" height="%s"><title>%s: %s</title></rect>`,
				number(x), number(top), number(barWidth*0.9), number(math.Max(height, 0.5)), escape(p.Label), escape(o.ValueFormat(p.Value)))
		}
		s.b.WriteString(`</g>`)
	}
	return s.close()
}

// StackedBar renders a stacked bar chart. | StackedBar 渲染堆叠柱状图。
func StackedBar(series []Series, o Options) string {
	if !finite(series) || len(series) == 0 {
		return empty(o, "bar-stacked")
	}
	o = o.normalize()
	labels := labelsOf(series[0])
	totals := make([]float64, len(labels))
	for _, ser := range series {
		for i, p := range ser.Points {
			if p.Value < 0 {
				// A stacked bar with a negative slice needs a diverging baseline; drawing it from
				// zero would silently misplace every following slice.
				// 含负值的堆叠柱需要一条发散基线；从零开始画会把后续每一片都悄悄放错位置。
				return empty(o, "bar-stacked")
			}
			if i < len(totals) {
				totals[i] += p.Value
			}
		}
	}
	max := 0.0
	for _, v := range totals {
		if v > max {
			max = v
		}
	}
	if o.YMax > o.YMin {
		max = o.YMax
	}
	if max <= 0 {
		max = 1
	}
	s := newSVG(o, "bar-stacked")
	s.frame(o.YMin, max, labels, len(labels))
	groupWidth := s.plotWidth() / float64(maxInt(len(labels), 1))
	barWidth := groupWidth * 0.55
	for i, label := range labels {
		base := 0.0
		for j, ser := range series {
			if i >= len(ser.Points) {
				continue
			}
			v := ser.Points[i].Value
			yTop := s.y(base+v, o.YMin, max)
			yBottom := s.y(base, o.YMin, max)
			class := seriesClass(ser, j)
			fmt.Fprintf(&s.b, `<rect class="yd-chart__bar %s" x="%s" y="%s" width="%s" height="%s"><title>%s %s: %s</title></rect>`,
				escape(class), number(s.left+float64(i)*groupWidth+(groupWidth-barWidth)/2), number(yTop), number(barWidth),
				number(math.Max(yBottom-yTop, 0.5)), escape(label), escape(ser.Label), escape(o.ValueFormat(v)))
			base += v
		}
	}
	return s.close()
}

// Pie renders a pie or donut chart. Each Point is one slice; Value must be positive. | Pie 渲染饼图或
// 环形图。每个 Point 是一个扇区；Value 必须为正。
func Pie(points []Point, o Options) string {
	o = o.normalize()
	total := 0.0
	for _, p := range points {
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || p.Value < 0 {
			return empty(o, "pie")
		}
		total += p.Value
	}
	if total <= 0 {
		return empty(o, "pie")
	}
	kind := "pie"
	if o.InnerRadius > 0 {
		kind = "donut"
	}
	s := newSVG(o, kind)
	cx := float64(o.Width) / 2
	cy := s.top + s.plotHeight()/2
	radius := math.Min(s.plotWidth(), s.plotHeight()) / 2 * 0.86
	inner := radius * o.InnerRadius
	start := -math.Pi / 2
	for i, p := range points {
		angle := p.Value / total * 2 * math.Pi
		end := start + angle
		class := fmt.Sprintf("yd-chart__series--%d", i%seriesClasses+1)
		d := arcPath(cx, cy, radius, inner, start, end)
		if d != "" {
			fmt.Fprintf(&s.b, `<path class="yd-chart__slice %s" d="%s"><title>%s: %s</title></path>`,
				escape(class), d, escape(p.Label), escape(o.ValueFormat(p.Value)))
		}
		start = end
	}
	return s.close()
}

// Donut is Pie with an inner radius. | Donut 是带内孔的 Pie。
func Donut(points []Point, o Options) string {
	if o.InnerRadius <= 0 {
		o.InnerRadius = 0.6
	}
	return Pie(points, o)
}

// Sparkline renders a compact line without axes, for a statistic tile. | Sparkline 渲染无轴的紧凑折线，
// 用于统计卡片。
func Sparkline(values []float64, o Options) string {
	o = o.normalize()
	points := make([]Point, 0, len(values))
	for i, v := range values {
		points = append(points, Point{Label: strconv.Itoa(i + 1), Value: v})
	}
	series := []Series{{Label: "", Points: points}}
	if !finite(series) || len(points) == 0 {
		return empty(o, "sparkline")
	}
	min, max := bounds(series, o)
	s := newSVG(o, "sparkline")
	s.left, s.right, s.top, s.bottom = 2, float64(o.Width)-2, 2, float64(o.Height)-2
	fmt.Fprintf(&s.b, `<path class="yd-chart__line" d="%s"/>`, s.linePath(points, min, max))
	return s.close()
}

// Gauge renders a semicircular gauge with the value in the centre. | Gauge 渲染半圆仪表盘，数值居中。
func Gauge(value, max float64, o Options) string {
	o = o.normalize()
	if max <= 0 || math.IsNaN(max) || math.IsInf(max, 0) || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return empty(o, "gauge")
	}
	if value > max {
		value = max
	}
	s := newSVG(o, "gauge")
	cx := float64(o.Width) / 2
	cy := s.bottom - 10
	radius := math.Min(s.plotWidth()/2, s.plotHeight()) * 0.86
	fmt.Fprintf(&s.b, `<path class="yd-chart__gauge-track" d="M %s %s A %s %s 0 0 1 %s %s" pathLength="100"/>`,
		number(cx-radius), number(cy), number(radius), number(radius), number(cx+radius), number(cy))
	pct := value / max * 100
	fmt.Fprintf(&s.b, `<path class="yd-chart__gauge-value" d="M %s %s A %s %s 0 0 1 %s %s" pathLength="100" stroke-dasharray="%s 100"><title>%s</title></path>`,
		number(cx-radius), number(cy), number(radius), number(radius), number(cx+radius), number(cy), number(pct), escape(o.ValueFormat(value)))
	s.text(cx, cy-8, "yd-chart__value", o.ValueFormat(value))
	if o.ShowAxis {
		s.text(cx, cy+22, "yd-chart__axis-text", o.ValueFormat(max))
	}
	return s.close()
}

// Radar renders a radar chart over named axes. | Radar 在命名轴上渲染雷达图。
func Radar(axes []string, series []Series, o Options) string {
	o = o.normalize()
	if len(axes) < 3 || len(series) == 0 || !finite(series) {
		return empty(o, "radar")
	}
	max := o.YMax
	if math.IsNaN(max) || math.IsInf(max, 0) {
		return empty(o, "radar")
	}
	if max <= 0 {
		for _, ser := range series {
			for _, p := range ser.Points {
				if p.Value > max {
					max = p.Value
				}
			}
		}
	}
	if max <= 0 {
		max = 1
	}
	s := newSVG(o, "radar")
	cx := float64(o.Width) / 2
	cy := s.top + s.plotHeight()/2
	radius := math.Min(s.plotWidth(), s.plotHeight()) / 2 * 0.78
	// Rings. | 网格环。
	for ring := 1; ring <= 4; ring++ {
		r := radius * float64(ring) / 4
		fmt.Fprintf(&s.b, `<polygon class="yd-chart__grid-ring" points="%s"/>`, polygonPoints(cx, cy, r, len(axes)))
	}
	// Axis spokes and labels. | 轴线与标签。
	for i, label := range axes {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(len(axes))
		x := cx + radius*math.Cos(angle)
		y := cy + radius*math.Sin(angle)
		fmt.Fprintf(&s.b, `<line class="yd-chart__grid" x1="%s" y1="%s" x2="%s" y2="%s"/>`, number(cx), number(cy), number(x), number(y))
		if s.o.ShowAxis {
			lx := cx + (radius+14)*math.Cos(angle)
			ly := cy + (radius+14)*math.Sin(angle)
			s.text(lx, ly, "yd-chart__axis-text", label)
		}
	}
	for i, ser := range series {
		class := seriesClass(ser, i)
		pts := make([]string, 0, len(axes))
		for j := range axes {
			v := 0.0
			if j < len(ser.Points) {
				v = ser.Points[j].Value
			}
			r := radius * clamp01(v/max)
			angle := -math.Pi/2 + float64(j)*2*math.Pi/float64(len(axes))
			pts = append(pts, number(cx+r*math.Cos(angle))+","+number(cy+r*math.Sin(angle)))
		}
		fmt.Fprintf(&s.b, `<polygon class="yd-chart__radar %s" points="%s"><title>%s</title></polygon>`,
			escape(class), escape(strings.Join(pts, " ")), escape(ser.Label))
	}
	return s.close()
}

// Composed renders bars and lines over one frame. | Composed 在同一画框中渲染柱与线。
func Composed(bars, lines []Series, o Options) string {
	o = o.normalize()
	all := append(append([]Series{}, bars...), lines...)
	if !finite(all) || len(all) == 0 {
		return empty(o, "composed")
	}
	min, max := bounds(all, o)
	s := newSVG(o, "composed")
	labels := labelsOf(all[0])
	s.frame(min, max, labels, len(labels))
	groupWidth := s.plotWidth() / float64(maxInt(len(labels), 1))
	barWidth := groupWidth * 0.5
	for i, ser := range bars {
		class := seriesClass(ser, i)
		for j, p := range ser.Points {
			x := s.left + float64(j)*groupWidth + (groupWidth-barWidth)/2
			y := s.y(p.Value, min, max)
			zero := s.y(0, min, max)
			fmt.Fprintf(&s.b, `<rect class="yd-chart__bar %s" x="%s" y="%s" width="%s" height="%s"><title>%s: %s</title></rect>`,
				escape(class), number(x), number(math.Min(y, zero)), number(barWidth), number(math.Max(math.Abs(zero-y), 0.5)),
				escape(p.Label), escape(o.ValueFormat(p.Value)))
		}
	}
	for i, ser := range lines {
		class := seriesClass(ser, len(bars)+i)
		fmt.Fprintf(&s.b, `<path class="yd-chart__line %s" d="%s"/>`, escape(class), s.linePath(ser.Points, min, max))
	}
	return s.close()
}

// Heatmap renders a matrix of cells; the opacity encodes the value. | Heatmap 渲染矩阵格子，透明度编码数值。
func Heatmap(cells [][]float64, xLabels, yLabels []string, o Options) string {
	o = o.normalize()
	if len(cells) == 0 {
		return empty(o, "heatmap")
	}
	min, max := 0.0, 0.0
	seen := false
	for _, row := range cells {
		for _, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return empty(o, "heatmap")
			}
			if !seen {
				min, max, seen = v, v, true
			}
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if max == min {
		max = min + 1
	}
	s := newSVG(o, "heatmap")
	cols := 0
	for _, row := range cells {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return s.close()
	}
	cellW := s.plotWidth() / float64(cols)
	cellH := s.plotHeight() / float64(maxInt(len(cells), 1))
	for ri, row := range cells {
		for ci, v := range row {
			opacity := 0.08 + 0.92*clamp01((v-min)/(max-min))
			x := s.left + float64(ci)*cellW
			y := s.top + float64(ri)*cellH
			label := ""
			if ci < len(xLabels) {
				label = xLabels[ci]
			}
			rowLabel := ""
			if ri < len(yLabels) {
				rowLabel = yLabels[ri]
			}
			fmt.Fprintf(&s.b, `<rect class="yd-chart__cell" x="%s" y="%s" width="%s" height="%s" opacity="%s"><title>%s %s: %s</title></rect>`,
				number(x), number(y), number(cellW*0.92), number(cellH*0.92), number(opacity), escape(rowLabel), escape(label), escape(o.ValueFormat(v)))
		}
	}
	return s.close()
}

// ProgressRing renders a circular progress indicator. | ProgressRing 渲染圆形进度。
func ProgressRing(value, max float64, o Options) string {
	if max <= 0 || math.IsNaN(max) || math.IsInf(max, 0) || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return empty(o, "ring")
	}
	if value > max {
		value = max
	}
	o = o.normalize()
	if o.Width < 120 {
		o.Width = 120
	}
	if o.Height < 120 {
		o.Height = 120
	}
	s := newSVG(o, "ring")
	cx := float64(o.Width) / 2
	cy := float64(o.Height) / 2
	r := math.Min(float64(o.Width), float64(o.Height))/2 - 8
	fmt.Fprintf(&s.b, `<circle class="yd-chart__ring-track" cx="%s" cy="%s" r="%s" pathLength="100"/>`, number(cx), number(cy), number(r))
	fmt.Fprintf(&s.b, `<circle class="yd-chart__ring-value" cx="%s" cy="%s" r="%s" pathLength="100" stroke-dasharray="%s 100"><title>%s</title></circle>`,
		number(cx), number(cy), number(r), number(value/max*100), escape(o.ValueFormat(value)))
	s.text(cx, cy+5, "yd-chart__value yd-chart__value--center", o.ValueFormat(value))
	return s.close()
}

// empty renders the shared empty plot. | empty 渲染共用的空图。
func empty(o Options, kind string) string {
	o = o.normalize()
	s := newSVG(o, kind)
	if o.EmptyText != "" {
		s.text(float64(o.Width)/2, float64(o.Height)/2, "yd-chart__empty", o.EmptyText)
	}
	return s.close()
}

// labelsOf returns the labels of the first series. | labelsOf 返回第一条序列的标签。
func labelsOf(s Series) []string {
	out := make([]string, 0, len(s.Points))
	for _, p := range s.Points {
		out = append(out, p.Label)
	}
	return out
}

// arcPath builds an arc (and the donut hole) for one slice. | arcPath 构建一个扇区的弧（以及环形内孔）。
func arcPath(cx, cy, radius, inner, start, end float64) string {
	if end-start >= 2*math.Pi-1e-9 {
		// A full circle cannot be expressed by one arc whose endpoints coincide; split it in two.
		// 端点重合的整圆不能用一段弧表示，拆成两段。
		mid := start + math.Pi
		ox1, oy1 := cx+radius*math.Cos(start), cy+radius*math.Sin(start)
		ox2, oy2 := cx+radius*math.Cos(mid), cy+radius*math.Sin(mid)
		if inner <= 0 {
			return fmt.Sprintf("M %s %s A %s %s 0 1 1 %s %s A %s %s 0 1 1 %s %s Z",
				number(ox1), number(oy1), number(radius), number(radius),
				number(ox2), number(oy2), number(radius), number(radius), number(ox1), number(oy1))
		}
		ix1, iy1 := cx+inner*math.Cos(start), cy+inner*math.Sin(start)
		ix2, iy2 := cx+inner*math.Cos(mid), cy+inner*math.Sin(mid)
		return fmt.Sprintf("M %s %s A %s %s 0 1 1 %s %s A %s %s 0 1 1 %s %s L %s %s A %s %s 0 1 0 %s %s A %s %s 0 1 0 %s %s Z",
			number(ox1), number(oy1), number(radius), number(radius),
			number(ox2), number(oy2), number(radius), number(radius), number(ox1), number(oy1),
			number(ix1), number(iy1), number(inner), number(inner),
			number(ix2), number(iy2), number(inner), number(inner), number(ix1), number(iy1))
	}
	x1 := cx + radius*math.Cos(start)
	y1 := cy + radius*math.Sin(start)
	x2 := cx + radius*math.Cos(end)
	y2 := cy + radius*math.Sin(end)
	large := 0
	if end-start > math.Pi {
		large = 1
	}
	if inner <= 0 {
		return fmt.Sprintf("M %s %s A %s %s 0 %d 1 %s %s L %s %s Z",
			number(x1), number(y1), number(radius), number(radius), large, number(x2), number(y2), number(cx), number(cy))
	}
	x3 := cx + inner*math.Cos(end)
	y3 := cy + inner*math.Sin(end)
	x4 := cx + inner*math.Cos(start)
	y4 := cy + inner*math.Sin(start)
	return fmt.Sprintf("M %s %s A %s %s 0 %d 1 %s %s L %s %s A %s %s 0 %d 0 %s %s Z",
		number(x1), number(y1), number(radius), number(radius), large, number(x2), number(y2),
		number(x3), number(y3), number(inner), number(inner), large, number(x4), number(y4))
}

// polygonPoints builds the points of a regular polygon. | polygonPoints 构建正多边形的点集。
func polygonPoints(cx, cy, r float64, n int) string {
	pts := make([]string, 0, n)
	for i := range n {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(n)
		pts = append(pts, number(cx+r*math.Cos(angle))+","+number(cy+r*math.Sin(angle)))
	}
	return strings.Join(pts, " ")
}

// clamp01 bounds a ratio to [0, 1]. | clamp01 把比例限制在 [0,1]。
func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// maxInt returns the larger of two ints. | maxInt 返回两个整数中较大的一个。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Legend returns the legend entries for a series set. | Legend 返回序列集合的图例项。
func Legend(series []Series) []LegendItem {
	out := make([]LegendItem, 0, len(series))
	for i, s := range series {
		out = append(out, LegendItem{Label: s.Label, Class: seriesClass(s, i)})
	}
	return out
}

// SliceLegend returns one legend entry per pie slice. | SliceLegend 为每个扇区返回一个图例项。
func SliceLegend(points []Point) []LegendItem {
	out := make([]LegendItem, 0, len(points))
	for i, p := range points {
		out = append(out, LegendItem{Label: p.Label, Class: fmt.Sprintf("yd-chart__series--%d", i%seriesClasses+1)})
	}
	return out
}
