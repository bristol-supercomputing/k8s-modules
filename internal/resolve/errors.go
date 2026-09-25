package resolve

import (
	"fmt"
	"strings"
)

type ErrDependencyNotFound struct{ Module, Missing string }

func (e ErrDependencyNotFound) Error() string {
	if e.Module == e.Missing {
		return fmt.Sprintf("module %q not found", e.Module)
	}
	return fmt.Sprintf("dependency %q of module %q not found", e.Missing, e.Module)
}

type ErrDependencyCycle struct{ Path []string }

func (e ErrDependencyCycle) Error() string {
	return "dependency cycle: " + strings.Join(e.Path, " -> ")
}

type ErrFamilyConflict struct {
	Family  string
	Modules []string
}

func (e ErrFamilyConflict) Error() string {
	return fmt.Sprintf("modules %s belong to the same family %q", quoted(e.Modules), e.Family)
}

type ErrEnvConflict struct {
	Name    string
	Modules []string
	Values  []string
}

func (e ErrEnvConflict) Error() string {
	return fmt.Sprintf("modules %s set %s to different values %s", quoted(e.Modules), e.Name, quoted(e.Values))
}

func quoted(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}
