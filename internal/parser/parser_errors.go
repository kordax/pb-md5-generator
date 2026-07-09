package parser

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/pseudomuto/protokit"
)

type SourceError struct {
	File   string
	Line   int
	Entity string
	Err    error
}

func (e SourceError) Error() string {
	location := e.File
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", e.File, e.Line)
	}
	if e.Entity != "" {
		return fmt.Sprintf("%s: %s: %s", location, e.Entity, e.Err.Error())
	}
	return fmt.Sprintf("%s: %s", location, e.Err.Error())
}

func (e SourceError) Unwrap() error {
	return e.Err
}

type markerError struct {
	marker string
	err    error
}

func (e markerError) Error() string {
	return e.err.Error()
}

func (e markerError) Unwrap() error {
	return e.err
}

func (p *DescriptorParser) messageError(descriptor *protokit.Descriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	var markerErr markerError
	if errors.As(err, &markerErr) {
		return p.sourceError(fileName, fmt.Sprintf("message %s marker %s", descriptor.GetName(), markerErr.marker), p.findMarker(fileName, markerErr.marker), err)
	}
	return p.sourceError(fileName, fmt.Sprintf("message %s", descriptor.GetName()), p.findDeclaration(fileName, "message", descriptor.GetName()), err)
}

func (p *DescriptorParser) fieldError(descriptor *protokit.FieldDescriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	return p.sourceError(fileName, fmt.Sprintf("field %s", descriptor.GetName()), p.findFieldDeclaration(fileName, descriptor.GetName()), err)
}

func (p *DescriptorParser) enumValueError(descriptor *protokit.EnumValueDescriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	return p.sourceError(fileName, fmt.Sprintf("enum value %s", descriptor.GetName()), p.findEnumValueDeclaration(fileName, descriptor.GetName()), err)
}

func (p *DescriptorParser) sourceError(fileName, entity string, index int, err error) error {
	return SourceError{
		File:   fileName,
		Line:   p.lineAt(fileName, index),
		Entity: entity,
		Err:    err,
	}
}

func (p *DescriptorParser) findDeclaration(fileName, kind, name string) int {
	payload := p.payloadForFile(fileName)
	return strings.Index(payload, kind+" "+name)
}

func (p *DescriptorParser) findFieldDeclaration(fileName, name string) int {
	payload := p.payloadForFile(fileName)
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(name) + `\b\s*=`)
	loc := re.FindStringIndex(payload)
	if loc == nil {
		return -1
	}
	return loc[0]
}

func (p *DescriptorParser) findMarker(fileName, marker string) int {
	return strings.Index(p.payloadForFile(fileName), marker)
}

func (p *DescriptorParser) findEnumValueDeclaration(fileName, name string) int {
	return p.findFieldDeclaration(fileName, name)
}

func (p *DescriptorParser) lineAt(fileName string, index int) int {
	if index < 0 {
		return 0
	}
	payload := p.payloadForFile(fileName)
	if payload == "" {
		return 0
	}
	if index > len(payload) {
		index = len(payload)
	}
	return strings.Count(payload[:index], "\n") + 1
}

func (p *DescriptorParser) payloadForFile(fileName string) string {
	if payload, ok := p.payload[fileName]; ok {
		return payload
	}
	file, ok := p.matchedFiles[fileName]
	if !ok || file == nil {
		return ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	readFile, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	payload := string(readFile)
	p.payload[fileName] = payload
	return payload
}
