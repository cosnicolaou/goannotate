package locate_test

import (
	"context"
	"go/build"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosnicolaou/goannotate/locate"
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
	locator := locate.New()
	err := locator.AddInterfaces(ctx,
		here+"data",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	err = locator.Do(ctx, build.Default, here+"data", here+"impl")
	if err != nil {
		t.Fatalf("locate.Do: %v", err)
	}

	compareLocations(t, locator.Functions(), []string{
		"(*" + here + "impl.Impl1).M1 implements " + implements("Ifc1"),
		"(*" + here + "impl.Impl1).M2 implements " + implements("Ifc1"),
		"(*" + here + "impl.Impl12).M1 implements " + implements("Ifc1", "Ifc2", "Ifc3"),
		"(*" + here + "impl.Impl12).M2 implements " + implements("Ifc1", "Ifc2", "Ifc3"),
		"(*" + here + "impl.Impl12).M3 implements " + implements("Ifc1", "Ifc2", "Ifc3"),
		"(*" + here + "impl.impl2).M3 implements " + implements("Ifc2"),
	}, []string{
		filepath.Join("impl", "impls.go") + ":5:1",
		filepath.Join("impl", "impls.go") + ":9:1",
		filepath.Join("impl", "impls.go:") + "22:1",
		filepath.Join("impl", "impls.go:") + "26:1",
		filepath.Join("impl", "impls.go:") + "30:1",
		filepath.Join("impl", "impls.go:") + "15:1",
	})
}
