// Mimics API of tinyxml2
package main

import (
	"log"
	"strings"
)

const XMLTrace = false

type XMLDocument struct {
	root XMLNode
}

type XMLNode interface {
	Print(sb *strings.Builder, level int)
}

type XMLElement struct {
	name string
	attributes map[string]string
	children []XMLNode
}

func (element *XMLElement) Print(sb *strings.Builder, level int) {
	sb.WriteString("<")
	sb.WriteString(element.name)
	for name, value := range element.attributes {
		sb.WriteString(" ")
		sb.WriteString(name)
		sb.WriteString("=")
		sb.WriteString("\"")
		WriteXmlEscapedStringIntoBuilder(sb, value)
		sb.WriteString("\"")
	}
	if len(element.children) == 0 {
		sb.WriteString("/>")
		return
	}
	sb.WriteString(">")
	for _, child := range element.children {
		child.Print(sb, level + 1)
	}
	sb.WriteString("</")
	sb.WriteString(element.name)
	sb.WriteString(">")
}

type XMLText struct {
	value string
}

func WriteXmlEscapedStringIntoBuilder(sb *strings.Builder, s string) {
	for _, x := range s {
		// https://stackoverflow.com/a/1091955
		switch x {
		case '"':	sb.WriteString("&quot;");
		case '&':	sb.WriteString("&amp;");
		case '\'':  sb.WriteString("&apos;");
		case '<':	sb.WriteString("&lt;");
		case '>':	sb.WriteString("&gt;");
		default:    sb.WriteRune(x);
		}
	}
}

func (text *XMLText) Print(sb *strings.Builder, level int) {
	WriteXmlEscapedStringIntoBuilder(sb, text.value)
}

func (doc *XMLDocument) NewElement(name string) *XMLElement {
	if XMLTrace {
		log.Printf("XMLTrace: XMLDocument.NewElement(name = %#v)\n", name)
	}
	element := new(XMLElement)
	element.name = name
	element.attributes = map[string]string{}
	return element
}

func (doc *XMLDocument) NewText(value string) *XMLText {
	if XMLTrace {
		log.Printf("XMLTrace: XMLDocument.NewText(value = %#v)\n", value)
	}
	text := new(XMLText)
	text.value = value
	return text
}

func (doc *XMLDocument) InsertFirstChild(child XMLNode) {
	if XMLTrace {
		log.Printf("XMLTrace: XMLDocument.InsertFirstChild(child)\n")
	}
	doc.root = child
}

func (doc *XMLDocument) String() string {
	sb := strings.Builder{}
	doc.root.Print(&sb, 0)
	return sb.String()
}

func (element *XMLElement) SetAttribute(name string, value string) {
	if XMLTrace {
		log.Printf("XMLTrace: XMLElement.SetAttribute(name = %#v, value = %#v)\n", name, value)
	}
	element.attributes[name] = value
}

func (element *XMLElement) SetText(value string) {
	if XMLTrace {
		log.Printf("XMLTrace: XMLElement.SetText(value = %#v)\n", value)
	}
	if len(element.children) > 0 {
		panic("XMLElement.SetText: expected not children")
	}
	text := new(XMLText)
	text.value = value
	element.InsertEndChild(text)
}

func (element *XMLElement) InsertEndChild(child XMLNode) {
	if XMLTrace {
		log.Printf("XMLTrace: XMLElement.InsertEndChild(child)")
	}
	element.children = append(element.children, child)
}

func (element *XMLElement) LinkEndChild(child XMLNode) {
	element.InsertEndChild(child)
}
