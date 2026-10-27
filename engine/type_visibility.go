package engine

import (
	"strings"

	"github.com/kordax/pb-md5-generator/internal/parser"
	"github.com/pseudomuto/protokit"
)

// Parsed entries omit ignored declarations. Inspect their original descriptor
// files to distinguish deliberately opaque types from visible linked types.
func ignoredTypeNames(files []ParsedFile) map[string]bool {
	ignored := make(map[string]bool)
	seen := make(map[*protokit.FileDescriptor]bool)
	enums := func(values []*protokit.EnumDescriptor, parentIgnored bool) {
		for _, value := range values {
			if value != nil && (parentIgnored || ignoredDescriptorComment(value.GetComments())) {
				ignored[strings.TrimPrefix(value.GetFullName(), ".")] = true
			}
		}
	}
	var messages func([]*protokit.Descriptor, bool)
	messages = func(values []*protokit.Descriptor, parentIgnored bool) {
		for _, value := range values {
			if value == nil {
				continue
			}
			hidden := parentIgnored || ignoredDescriptorComment(value.GetComments())
			if hidden {
				ignored[strings.TrimPrefix(value.GetFullName(), ".")] = true
			}
			enums(value.GetEnums(), hidden)
			messages(value.GetMessages(), hidden)
		}
	}
	var visitFile func(*protokit.FileDescriptor)
	visitFile = func(file *protokit.FileDescriptor) {
		if file == nil || seen[file] {
			return
		}
		seen[file] = true
		hidden := ignoredDescriptorComment(file.GetPackageComments())
		enums(file.GetEnums(), hidden)
		messages(file.GetMessages(), hidden)
		for _, dependency := range file.GetImports() {
			if dependency != nil {
				visitFile(dependency.GetFile())
			}
		}
	}
	var entries func([]Entry)
	entries = func(values []Entry) {
		for _, value := range values {
			if value.msg != nil && value.msg.m != nil {
				visitFile(value.msg.m.GetFile())
				entries(value.msg.entries)
			}
			if value.enum != nil && value.enum.e != nil {
				visitFile(value.enum.e.GetFile())
			}
		}
	}
	for _, file := range files {
		entries(file.entries)
	}
	return ignored
}

func ignoredDescriptorComment(comment *protokit.Comment) bool {
	if comment == nil {
		return false
	}
	if parser.CommentIgnored(comment.GetLeading()) || parser.CommentIgnored(comment.GetTrailing()) {
		return true
	}
	for _, detached := range comment.GetDetached() {
		if parser.CommentIgnored(detached) {
			return true
		}
	}
	return false
}
