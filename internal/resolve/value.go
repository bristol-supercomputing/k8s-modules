package resolve

import (
	"fmt"
	"strconv"
	"strings"
)

// ModuleValue is one parsed resolved.modules.isambard.ac.uk/<family> value, the inverse of AnnotationValue.
type ModuleValue struct {
	Name  string
	Rank  int
	Src   string
	Mount string
	Lib   []string
	Mask  []string
}

// ParseValue parses "name;rank=N;src=S;mount=M[;lib=a:b][;mask=a:b]".
func ParseValue(v string) (ModuleValue, error) {
	parts := strings.Split(v, ";")
	mv := ModuleValue{Name: parts[0], Rank: -1}
	for _, p := range parts[1:] {
		k, val, ok := strings.Cut(p, "=")
		if !ok {
			return mv, fmt.Errorf("malformed module annotation field %q", p)
		}
		switch k {
		case "rank":
			n, err := strconv.Atoi(val)
			if err != nil {
				return mv, fmt.Errorf("malformed rank %q", val)
			}
			mv.Rank = n
		case "src":
			mv.Src = val
		case "mount":
			mv.Mount = val
		case "lib":
			mv.Lib = strings.Split(val, ":")
		case "mask":
			mv.Mask = strings.Split(val, ":")
		}
	}
	if mv.Name == "" || mv.Rank < 0 || mv.Src == "" || mv.Mount == "" {
		return mv, fmt.Errorf("module annotation %q lacks name, rank, src or mount", v)
	}
	return mv, nil
}
