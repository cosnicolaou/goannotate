package find_test

import (
	"context"
	"go/build"
	"strings"
	"testing"

	"github.com/cosnicolaou/goannotate/find"
)

func implements(ifcs ...string) string {
	out := strings.Builder{}
	for i, ifc := range ifcs {
		out.WriteString(here)
		out.WriteString("data.")
		out.WriteString(ifc)
		if i < len(ifcs)-1 {
			out.WriteString(", ")
		}
	}
	return out.String()
}
func TestImpl(t *testing.T) {
	ctx := context.Background()
	here := "github.com/cosnicolaou/goannotate/find/internal/"
	finder := find.New()
	err := finder.AddInterfaces(ctx,
		here+"data.*",
	)
	if err != nil {
		t.Fatalf("find.AddInterfaces: %v", err)
	}
	err = finder.FindInPkgs(ctx, build.Default, here+"...")
	if err != nil {
		t.Fatalf("find.FindInPkgs: %v", err)
	}

	compareLocations(t, finder.AnnotationLocations(), []string{
		"func (*" + here + "impl.Impl1).M1() implements interface " + implements("Ifc1"),
		"func (*" + here + "impl.Impl1).M2(string) implements interface " + implements("Ifc1"),
		"func (*" + here + "impl.Impl12).M1() implements interface " + implements("Ifc1", "Ifc2", "Ifc3"),
		"func (*" + here + "impl.Impl12).M2(string) implements interface " + implements("Ifc1", "Ifc2", "Ifc3"),
		"func (*" + here + "impl.Impl12).M3(int) error implements interface " + implements("Ifc1", "Ifc2", "Ifc3"),
		"func (*" + here + "impl.impl2).M3(int) error implements interface " + implements("Ifc2"),
	}, []string{
		here + "impl/impls.go:5:17",
		here + "impl/impls.go:9:17",
		here + "impl/impls.go:22:18",
		here + "impl/impls.go:26:18",
		here + "impl/impls.go:30:18",
		here + "impl/impls.go:15:17",
	})
}
