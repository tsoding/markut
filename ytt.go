// MIT License
//
// Copyright (c) 2025 Kam1k4dze
// Copyright (c) 2026 rexim
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Ported from https://github.com/Kam1k4dze/SubChat commit 21e18e3ccb9e16ce9b11cf68b9a54d84d2858317

package main

import (
	"unicode/utf8"
	"strings"
	"strconv"
	"fmt"
)

const MaxValue = 254

type Color struct {
	r byte
	g byte
	b byte
	a byte
}

func hexToInt(c byte) int {
	if (c >= '0' && c <= '9') {
		return int(c - '0')
	}
	if (c >= 'A' && c <= 'F') {
		return int(c - 'A' + 10)
	}
	if (c >= 'a' && c <= 'f') {
		return int(c - 'a' + 10)
	}
	return 0
}

func parseHexColor(hex string) Color {
	if len(hex) == 0 {
		return Color{}
	}

	cleaned := hex
	if (cleaned[0] == '#') {
		cleaned = cleaned[1:]
	}

	// Convert to uppercase for consistency
	cleaned = strings.ToUpper(cleaned)

	var r, g, b, a uint8

	// Support formats:
	// - #RGB : 3-digit, assume opaque (alpha = maxValue)
	// - #RGBA : 4-digit, includes alpha
	// - #RRGGBB : 6-digit, assume opaque
	// - #RRGGBBAA : 8-digit, includes alpha
	if len(cleaned) == 3 {
		r = uint8(hexToInt(cleaned[0]) * 16 + hexToInt(cleaned[0]));
		g = uint8(hexToInt(cleaned[1]) * 16 + hexToInt(cleaned[1]));
		b = uint8(hexToInt(cleaned[2]) * 16 + hexToInt(cleaned[2]));
		a = MaxValue;
	} else if (len(cleaned) == 4) {
		r = uint8(hexToInt(cleaned[0]) * 16 + hexToInt(cleaned[0]));
		g = uint8(hexToInt(cleaned[1]) * 16 + hexToInt(cleaned[1]));
		b = uint8(hexToInt(cleaned[2]) * 16 + hexToInt(cleaned[2]));
		a = uint8(hexToInt(cleaned[3]) * 16 + hexToInt(cleaned[3]));
	} else if (len(cleaned) == 6) {
		r = uint8(hexToInt(cleaned[0]) * 16 + hexToInt(cleaned[1]));
		g = uint8(hexToInt(cleaned[2]) * 16 + hexToInt(cleaned[3]));
		b = uint8(hexToInt(cleaned[4]) * 16 + hexToInt(cleaned[5]));
		a = MaxValue;
	} else if (len(cleaned) == 8) {
		r = uint8(hexToInt(cleaned[0]) * 16 + hexToInt(cleaned[1]));
		g = uint8(hexToInt(cleaned[2]) * 16 + hexToInt(cleaned[3]));
		b = uint8(hexToInt(cleaned[4]) * 16 + hexToInt(cleaned[5]));
		a = uint8(hexToInt(cleaned[6]) * 16 + hexToInt(cleaned[7]));
	}
	return Color{r, g, b, a}
}

func (color Color) toHexString() string {
	if color.a != MaxValue {
		return fmt.Sprintf("#%02X%02X%02X%02X", color.r, color.g, color.b, color.a);
	}
	return fmt.Sprintf("#%02X%02X%02X", color.r, color.g, color.b);
}

func (color Color) String() string {
	return color.toHexString()
}

type User struct {
	name string
	color Color
}

// A single wrapped chat line.
type ChatLine struct {
	user *User
	text string
}

type EdgeType int
const (
	None EdgeType = iota
	HardShadow
	Bevel
	GlowOutline
	SoftShadow
)

type FontStyle int
const (
	Default FontStyle = iota
	Monospaced // Courier New
	Proportional // Times New Roman
	MonospacedSans // Lucida Console
	ProportionalSans // Roboto
	Casual // Comic Sans!
	Cursive // Monotype Corsiva
	SmallCapitals // (Arial with font-variant: small-caps)
)

type TextAlignment int
const (
	Left TextAlignment = iota
	Right
	Center
)

type ChatParams struct {
	textBold bool
	textItalic bool
	textUnderline bool

	textForegroundColor Color
	textBackgroundColor Color
	textEdgeColor Color

	textEdgeType EdgeType
	fontStyle FontStyle
	fontSizePercent int

	textAlignment TextAlignment
	horizontalMargin int
	verticalSpacing int

	usernameSeparator string
	maxCharsPerLine int
	totalDisplayLines int
}

var DefaultChatParams = ChatParams{
	textForegroundColor: Color{254, 254, 254, 254},
	textBackgroundColor: Color{254, 254, 254, 0},
	textEdgeColor:       Color{0, 0, 0, 254},

	textEdgeType: SoftShadow,
	fontStyle: MonospacedSans,

	textAlignment: Left,
	horizontalMargin: 71,
	verticalSpacing: -1,

	usernameSeparator: ": ",
	maxCharsPerLine: 25,
	totalDisplayLines: 13,
}

type Batch struct {
	time Millis
	lines []ChatLine
}

// A single parsed chat message.
type YttChatMessage struct {
	time Millis
	user User
	message string
};

func generateBatches(messages []YttChatMessage, params ChatParams) []Batch {
	batches := []Batch{}
	currentLines := []ChatLine{}
	for _, msg := range messages {
		username, wrapped := wrapMessage(msg.user.name, params.usernameSeparator, msg.message, params.maxCharsPerLine)
		if len(wrapped) == 0 {
			continue
		}

		currentLines = append(currentLines, ChatLine{
			user: &User{
				username,
				msg.user.color,
			},
			text: wrapped[0],
		})
		if len(currentLines) > params.totalDisplayLines {
			currentLines = currentLines[1:]
		}
		for i := 1; i < len(wrapped); i += 1 {
			currentLines = append(currentLines, ChatLine{
				user: nil,
				text: wrapped[i],
			})
			if len(currentLines) > params.totalDisplayLines {
				currentLines = currentLines[1:]
			}
		}
		if len(batches) > 0 && batches[len(batches) - 1].time == msg.time {
			continue
		}
		batches = append(batches, Batch{
			time: msg.time,
			lines: currentLines,
		})
	}
	return batches
}

func wrapMessage(username string, separator string, message string, maxWidth int) (string, []string) {
	lines := []string{}
	availableSpace := maxWidth
	if (utf8_length(username) > maxWidth) {
		username = utf8_substr(username, maxWidth)
		lines = append(lines, "")
	} else {
		availableSpace -= utf8_length(username)
	}

	if (utf8_length(separator) > availableSpace) {
		separator = utf8_substr(separator, availableSpace)
	}
	lines = append(lines, separator)
	availableSpace -= utf8_length(separator)

	firstWord := true
	for _, word := range strings.Fields(message) {
		bigWord := false
		for utf8_length(word) > maxWidth {
			bigWord = true
			if (availableSpace < 2) {
				availableSpace = maxWidth
				lines = append(lines, utf8_substr(word, availableSpace))
				firstWord = false
			} else {
				if (!firstWord) {
					lines[len(lines)-1] += " "
					availableSpace--
				}
				lines[len(lines)-1] += utf8_substr(word, availableSpace)
				firstWord = false
			}
			word = utf8_consume(word, availableSpace) // add split
			availableSpace = 0
		}
		if (bigWord) {
			//if (utf8_length(word) < availableSpace) word += " "
			lines = append(lines, word)
			availableSpace = maxWidth - utf8_length(word)
			firstWord = false
			continue
		}
		if (utf8_length(word) < availableSpace) {
			if (!firstWord) {
				lines[len(lines)-1] += " "
				availableSpace--
			}
			lines[len(lines)-1] += word
			availableSpace -= utf8_length(word)
		} else {
			//if (utf8_length(word) < maxWidth) word += " "
			lines = append(lines, word)
			availableSpace = maxWidth - utf8_length(word)
		}
		firstWord = false
	}
	//	for (const auto& line :lines){
	//		assert(utf8_length(line)<=maxWidth)
	//	}
	return username, lines
}

func generateXML(batches []Batch, params ChatParams) string {
	var doc XMLDocument

	colors := map[Color]string{}
	colors[params.textForegroundColor] = ""
	// Not optimal, but I want to factor out messages
	for _, m := range batches {
		for _, l := range m.lines {
			if l.user != nil {
				colors[l.user.color] = ""
			}
		}
	}

	root := doc.NewElement("timedtext")
	root.SetAttribute("format", "3")
	doc.InsertFirstChild(root)

	head := doc.NewElement("head")
	root.InsertEndChild(head)
	body := doc.NewElement("body")
	root.InsertEndChild(body)

	// Create pen elements for each unique color.
	penIndex := 0
	for color := range colors {
		pen := doc.NewElement("pen")
		pen.SetAttribute("id", strconv.Itoa(penIndex))
		if params.textBold {
			pen.SetAttribute("b", "1")
		} else {
			pen.SetAttribute("b", "0")
		}
		if params.textItalic {
			pen.SetAttribute("i", "1")
		} else {
			pen.SetAttribute("i", "0")
		}
		if params.textUnderline {
			pen.SetAttribute("u", "1")
		} else {
			pen.SetAttribute("u", "0")
		}

		// Use the friendly textForegroundColor if it differs from default white.
		pen.SetAttribute("fc", color.String())
		pen.SetAttribute("fo", strconv.Itoa(int(params.textForegroundColor.a)))
		pen.SetAttribute("bc", params.textBackgroundColor.String())
		pen.SetAttribute("bo", strconv.Itoa(int(params.textBackgroundColor.a)))

		// Set edge attributes if provided.
		textEdgeType  := strconv.Itoa(int(params.textEdgeType))
		if len(textEdgeType) != 0 {
			pen.SetAttribute("ec", params.textEdgeColor.String())
			pen.SetAttribute("et", textEdgeType)
		}

		pen.SetAttribute("fs", strconv.Itoa(int(params.fontStyle)))
		pen.SetAttribute("sz", strconv.Itoa(params.fontSizePercent))
		head.InsertEndChild(pen)
		colors[color] = strconv.Itoa(penIndex)
		penIndex++
	}

	// Create workspace element for whatever reason.
	ws := doc.NewElement("ws")
	ws.SetAttribute("id", "1") // default workspace id
	ws.SetAttribute("ju", strconv.Itoa(int(params.textAlignment)))
	head.InsertEndChild(ws)

	// Create window position (wp) elements.
	for i := 0; i < params.totalDisplayLines; i += 1 {
		wp := doc.NewElement("wp")
		wp.SetAttribute("id", strconv.Itoa(i))
		wp.SetAttribute("ap", "0") // anchor point
		wp.SetAttribute("ah", strconv.Itoa(params.horizontalMargin))
		wp.SetAttribute("av", strconv.Itoa(i * params.verticalSpacing))
		head.InsertEndChild(wp)
	}
	// Zero-width space (ZWSP) as a UTF-8 string.
	defaultPen := colors[params.textForegroundColor]
	const ZWSP = "\xE2\x80\x8B"
	for batchIndex := 0; batchIndex + 1 < len(batches); batchIndex += 1 {
		batch := batches[batchIndex]
		nextBatch := batches[batchIndex + 1]
		if (params.verticalSpacing == -1) {
			pElem := doc.NewElement("p")
			pElem.SetAttribute("t", strconv.FormatInt(int64(batch.time), 10))
			duration := nextBatch.time - batch.time
			pElem.SetAttribute("d", strconv.FormatInt(int64(duration), 10))
			pElem.SetAttribute("wp", "0")
			pElem.SetAttribute("ws", "1")
			pElem.SetAttribute("p", defaultPen)
			pElem.LinkEndChild(doc.NewText(""))

			for _, line := range batch.lines {
				if line.user != nil {
					sUser := doc.NewElement("s")
					sUser.SetAttribute("p", colors[line.user.color])
					userText := line.user.name
					sUser.SetText(userText)
					pElem.InsertEndChild(sUser)
					pElem.LinkEndChild(doc.NewText(ZWSP))
				}
				sText := doc.NewElement("s")
				sText.SetAttribute("p", defaultPen)
				sText.SetText(line.text)
				pElem.InsertEndChild(sText)
				pElem.LinkEndChild(doc.NewText("\n"))
			}
			body.InsertEndChild(pElem)
		} else {
			for idx, line := range batch.lines {
				pElem := doc.NewElement("p")
				pElem.SetAttribute("t", strconv.FormatInt(int64(batch.time), 10))
				duration := nextBatch.time - batch.time
				pElem.SetAttribute("d", strconv.FormatInt(int64(duration), 10))
				pElem.SetAttribute("wp", strconv.Itoa(idx))
				pElem.SetAttribute("ws", "1")
				pElem.SetAttribute("p", defaultPen)

				pElem.LinkEndChild(doc.NewText(""))
				if line.user != nil {
					sUser := doc.NewElement("s")
					sUser.SetAttribute("p", colors[line.user.color])
					userText := line.user.name
					sUser.SetText(userText)
					pElem.InsertEndChild(sUser)
					pElem.LinkEndChild(doc.NewText(ZWSP))
				}

				sText := doc.NewElement("s")
				sText.SetAttribute("p", defaultPen)
				sText.SetText(line.text)
				pElem.InsertEndChild(sText)
				pElem.LinkEndChild(doc.NewText(""))

				body.InsertEndChild(pElem)
			}
		}
	}

	return doc.String()
}

// Returns the number of UTF‑8 code points in s.
func utf8_length(s string) int {
	return utf8.RuneCountInString(s)
}

// Returns the first 'count' UTF‑8 code points of s.
func utf8_substr(s string, count int) string {
	return string([]rune(s)[:count])
}

// Returns the remainder of s after consuming the first 'count' UTF‑8 code points.
func utf8_consume(s string, count int) string {
	return string([]rune(s)[count:]);
}
