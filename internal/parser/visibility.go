package parser

import "strings"

// CommentIgnored reports a real @ignore or @doc ignore flag in one documentation
// comment. Examples and quoted text are not directives. Check independent
// detached, leading and trailing SourceCodeInfo comments separately, then OR the
// results so an example in one comment cannot consume a directive in another.
// Malformed annotations return false; normal parsing reports their syntax error.
func CommentIgnored(comment string) bool {
	annotations, err := parseCommentAnnotations(comment)
	if err != nil {
		return false
	}
	for _, annotation := range annotations {
		if annotation.name == IgnoreMarker {
			return true
		}
		if annotation.name == DocumentAnnotationMarker {
			for _, flag := range annotation.flags {
				if flag == IgnoreMarker {
					return true
				}
			}
		}
	}
	return false
}

// indentedCommentLine preserves Markdown's four-column code indentation after
// removing a raw protobuf comment delimiter and its conventional single space.
func indentedCommentLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	content := line
	switch {
	case strings.HasPrefix(trimmed, "//"), strings.HasPrefix(trimmed, "/*"):
		content = strings.TrimPrefix(trimmed[2:], " ")
	case strings.HasPrefix(trimmed, "*"):
		content = strings.TrimPrefix(trimmed[1:], " ")
	}
	columns := 0
	for _, character := range content {
		switch character {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return false
		}
		if columns >= 4 {
			return true
		}
	}
	return false
}
