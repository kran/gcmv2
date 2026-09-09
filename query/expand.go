package query

import (
	"fmt"
	"strings"
)

const (
	MaxExpandBytes = 1024
	MaxExpandPaths = 32
	MaxExpandDepth = 4
)

// ExpandPath is one typed relation path. Every segment is either PathOutRef or
// PathInRef; core resolves the next Type after every hop.
type ExpandPath []Path

// Expand constructs one relation expansion path.
func Expand(segments ...Path) ExpandPath {
	return append(ExpandPath(nil), segments...)
}

// ParseExpand parses the trusted textual frontend used by Admin and templates.
// Incoming segments must use <-source_type.field; nested segments use dots.
func ParseExpand(expression string) ([]ExpandPath, error) {
	if len(expression) > MaxExpandBytes {
		return nil, fmt.Errorf("query: expand exceeds %d bytes", MaxExpandBytes)
	}
	var paths []ExpandPath
	for rawPath := range strings.SplitSeq(expression, ",") {
		rawPath = strings.TrimSpace(rawPath)
		if rawPath == "" {
			continue
		}
		parts := strings.Split(rawPath, ".")
		path := make(ExpandPath, 0, len(parts))
		for i := 0; i < len(parts); {
			part := strings.TrimSpace(parts[i])
			if part == "" {
				return nil, fmt.Errorf("query: expand path %q has empty segment", rawPath)
			}
			if after, ok := strings.CutPrefix(part, "<-"); ok {
				sourceType := after
				if sourceType == "" || i+1 >= len(parts) {
					return nil, fmt.Errorf("query: incoming expand must use <-type.field")
				}
				field := strings.TrimSpace(parts[i+1])
				if field == "" || strings.HasPrefix(field, "<-") || strings.HasPrefix(field, "->") {
					return nil, fmt.Errorf("query: incoming expand must use <-type.field")
				}
				path = append(path, Incoming(sourceType, field))
				i += 2
				continue
			}
			path = append(path, Ref(strings.TrimPrefix(part, "->")))
			i++
		}
		if len(path) > MaxExpandDepth {
			return nil, fmt.Errorf("query: expand path %q exceeds depth %d", rawPath, MaxExpandDepth)
		}
		if len(paths) >= MaxExpandPaths {
			return nil, fmt.Errorf("query: expand exceeds %d paths", MaxExpandPaths)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("query: expand expression is empty")
	}
	return paths, nil
}

// ExpandKey is the stable response key for one relation segment.
func ExpandKey(path Path) string {
	if path.Kind == PathInRef {
		return "<-" + path.SourceType + "." + path.Field
	}
	return path.Field
}
