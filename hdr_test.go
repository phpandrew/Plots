package main

import "testing"

func TestHeaderModes(t *testing.T) {
	for h := 0; h < 4; h++ {
		RenderPlot(RenderOpts{W: 300, H: 200, Scale: 1.25, TitleH: 40, Title: "x", Color: ParseHex("#336699"), Opacity: 50, TitlePt: 16, Header: h, HeaderCol: ParseHex("#ff0000"), TitleCol: ParseHex("#00ff00"), FontFile: "/nope.ttf"})
	}
}
