package locate_test

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cloudeng.io/errors"
	"github.com/cosnicolaou/goannotate/locate"
)

func compareLocations(t *testing.T, value string, prefixes, suffixes []string) {
	loc := errors.Caller(2, 1)
	locations := strings.Split(value, "\n")
	if got, want := len(locations), len(suffixes)+1; got != want {
		t.Errorf("%v: got %v, want %v", loc, got, want)
		return
	}
	sort.Strings(locations)
	for i, l := range locations {
		if i == 0 {
			// empty line comes first after sorting.
			if got, want := len(l), 0; got != want {
				t.Errorf("%v: got %v, want %v", loc, got, want)
			}
			continue
		}
		if got, want := l, prefixes[i-1]; !strings.HasPrefix(got, want) {
			t.Errorf("%v: %v doesn't start with %v", loc, got, want)
		}
		if got, want := l, suffixes[i-1]; !strings.HasSuffix(got, want) {
			t.Errorf("%v: got %v doesn't have suffix %v", loc, got, want)
		}
	}
}

func compareFiles(t *testing.T, files string, expected ...string) {
	loc := errors.Caller(2, 1)
	found := strings.Split(files, "\n")
	sort.Strings(found)
	if got, want := len(found), len(expected)+1; got != want {
		t.Errorf("%v: got %v, want %v", loc, got, want)
		return
	}
	for i, f := range found {
		if i == 0 {
			// empty line comes first after sorting.
			if got, want := len(f), 0; got != want {
				t.Errorf("%v: got %v, want %v", loc, got, want)
			}
			continue
		}
		if got, want := f, expected[i-1]; !strings.Contains(got, want) {
			t.Errorf("%v: got %v doesn't have suffix %v", loc, got, want)
		}
	}
}

func compareSlices(t *testing.T, got, want []string) {
	if got, want := len(got), len(want); got != want {
		t.Errorf("%v: got %v, want %v", errors.Caller(2, 1), got, want)
		return
	}
	for i := range got {
		if got, want := got[i], want[i]; !strings.HasSuffix(got, want) {
			t.Errorf("%v: got %v does not end with %v", errors.Caller(2, 1), got, want)
			return
		}
	}
}

const here = "github.com/cosnicolaou/goannotate/locate/testdata/"

func TestInterfaces(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()
	err := locator.AddInterfaces(ctx,
		here+"data.xxxx",
		here+"data.Ifc2",
	)
	if err == nil {
		t.Errorf("failed to detect missing interface")
	}
	err = locator.AddInterfaces(ctx,
		here+"data.Ifc1",
		here+"data.Ifc2",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	compareLocations(t, locator.Interfaces(), []string{
		here + "data.Ifc1",
		here + "data.Ifc2",
	}, []string{
		filepath.Join("data", "interfaces.go") + ":3:6",
		filepath.Join("data", "interfaces.go") + ":12:6",
	})
	err = locator.AddInterfaces(ctx,
		here+"data",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	compareLocations(t, locator.Interfaces(), []string{
		here + "data.Ifc1",
		here + "data.Ifc2",
		here + "data.Ifc3",
	}, []string{
		filepath.Join("data", "interfaces.go") + ":3:6",
		filepath.Join("data", "interfaces.go") + ":12:6",
		filepath.Join("data", "interfaces.go") + ":16:6",
	})
	compareFiles(t, locator.Files(), "data/interfaces.go: data")
}

func TestEmbeddedInterfaces(t *testing.T) {
	ctx := context.Background()

	locator := locate.New(locate.IgnoreMissingFuctionsEtc())
	err := locator.AddInterfaces(ctx,
		here+"data/embedded.StructEmbed",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	if got, want := locator.Interfaces(), ""; got != want {
		t.Errorf("got %v, want %v", got, want)
	}

	locator = locate.New()
	err = locator.AddInterfaces(ctx,
		here+"data/embedded.IfcE$",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	compareLocations(t, locator.Interfaces(), []string{
		here + "data/embedded.IfcE",
		here + "data/embedded.IfcE1",
		here + "data/embedded.IfcE2",
		here + "data/embedded.ifcE3",
		here + "data/embedded/pkg.Pkg",
	}, []string{
		filepath.Join("data", "embedded", "embedded.go") + ":18:6",
		filepath.Join("data", "embedded", "embedded.go") + ":5:6",
		filepath.Join("data", "embedded", "embedded.go") + ":9:6",
		filepath.Join("data", "embedded", "embedded.go") + ":13:6",
		filepath.Join("data", "embedded", "pkg", "interface.go") + ":3:6",
	})
	compareFiles(t, locator.Files(),
		filepath.Join("data", "embedded", "embedded.go")+": embedded",
		filepath.Join("data", "embedded", "pkg", "interface.go")+": pkg",
	)

}

func TestFunctions(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()

	err := locator.AddFunctions(ctx, here+"data.nothere", "notthere")
	if err == nil {
		t.Errorf("failed to detect missing function")
	}

	err = locator.AddFunctions(ctx,
		here+"data.Fn2$",
	)
	if err != nil {
		t.Fatalf("locate.AddFunctions: %v", err)
	}
	compareLocations(t, locator.Functions(), []string{
		here + "data.Fn2",
	}, []string{
		here + "data/functions_more.go:3:1",
	})

	err = locator.AddFunctions(ctx,
		here+"data",
	)
	if err != nil {
		t.Fatalf("locate.AddFunctions: %v", err)
	}
	compareLocations(t, locator.Functions(), []string{
		here + "data.Fn1",
		here + "data.Fn2",
	}, []string{
		"data/functions.go:7:1",
		"data/functions_more.go:3:1",
	})
	compareLocations(t, locator.Functions(), []string{
		here + "data.Fn1",
		here + "data.Fn2",
	}, []string{
		"data/functions.go:7:1",
		"data/functions_more.go:3:1",
	})
	compareFiles(t, locator.Files(),
		filepath.Join("data", "functions.go")+": data",
		filepath.Join("data", "functions_more.go")+": data",
	)
}

func TestFunctionsAndInterfaces(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()
	err := locator.AddFunctions(ctx, here+"data.Fn2$")
	if err != nil {
		t.Fatalf("locate.AddFunctions: %v", err)
	}
	err = locator.AddInterfaces(ctx, here+"data.Ifc2$")
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	compareLocations(t, locator.Interfaces(), []string{
		here + "data.Ifc2 interface",
	}, []string{
		filepath.Join("data", "interfaces.go") + ":12:6",
	})
	compareLocations(t, locator.Functions(), []string{
		here + "data.Fn2",
	}, []string{
		filepath.Join("data", "functions_more.go") + ":3:1",
	})
}

func TestMultiPackageError(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()
	err := locator.AddInterfaces(ctx, here+"multipackage")
	if err == nil || !strings.Contains(err.Error(), "failed to type check: github.com/cosnicolaou/goannotate/locate/testdata/multipackage") {
		t.Fatalf("expected a specific error, but got: %v", err)
	}
	err = locator.AddInterfaces(ctx, here+"parseerror")
	if err == nil || !strings.Contains(err.Error(), "failed to type check: github.com/cosnicolaou/goannotate/locate/testdata/parseerror") {
		t.Fatalf("expected a specific error, but got: %v", err)
	}
	err = locator.AddInterfaces(ctx, here+"typeerror")
	if err == nil || !strings.Contains(err.Error(), "failed to type check: github.com/cosnicolaou/goannotate/locate/testdata/typeerror") {
		t.Fatalf("expected a specific error, but got: %v", err)
	}
	err = locator.AddInterfaces(ctx, here+"multipackage.(")
	if err == nil || !strings.Contains(err.Error(), "failed to compile regexp") {
		t.Fatalf("expected a specific error, but got: %v", err)
	}
}

func TestImports(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()
	err := locator.AddFunctions(ctx, here+"imports", here+"data")
	if err != nil {
		t.Fatalf("locate.AddFunctions: %v", err)
	}
	start, stop := []string{}, []string{}
	locator.WalkFiles(func(fullname string, fset *token.FileSet, file *ast.File) {
		begin, end := locate.ImportBlock(file)
		start = append(start, fset.Position(begin).String())
		stop = append(stop, fset.Position(end).String())
	})
	startAt := []string{
		"-",
		"-",
		"blocks.go:3:1",
		"import.go:3:1",
		"imports.go:3:1",
	}
	stopAt := []string{
		"-",
		"-",
		"blocks.go:8:2",
		"import.go:3:13",
		"imports.go:6:2",
	}
	compareSlices(t, start, startAt)
	compareSlices(t, stop, stopAt)
}

func TestFunctionDecls(t *testing.T) {
	ctx := context.Background()
	locator := locate.New()
	err := locator.AddFunctions(ctx, here+"data")
	if err != nil {
		t.Fatalf("locate.AddFunctions: %v", err)
	}
	err = locator.AddInterfaces(ctx,
		here+"data",
	)
	if err != nil {
		t.Fatalf("locate.AddInterfaces: %v", err)
	}
	err = locator.Do(ctx, here+"data", here+"impl")
	if err != nil {
		t.Fatalf("locate.Do: %v", err)
	}
	start, stop := []string{}, []string{}
	locator.WalkFunctions(func(fullname string, fset *token.FileSet, info *types.Info, fn *types.Func, decl *ast.FuncDecl, implemented []string) {
		begin := decl.Body.Pos()
		end := decl.Body.End()
		start = append(start, fset.Position(begin).String())
		stop = append(stop, fset.Position(end).String())
	})
	startAt := []string{
		"functions.go:7:18",
		"functions_more.go:3:23",
		"impls.go:5:22",
		"impls.go:9:28",
		"impls.go:15:31",
		"impls.go:22:23",
		"impls.go:26:29",
		"impls.go:30:32",
	}
	stopAt := []string{
		"functions.go:9:2",
		"functions_more.go:5:2",
		"impls.go:7:2",
		"impls.go:11:2",
		"impls.go:18:2",
		"impls.go:24:2",
		"impls.go:28:2",
		"impls.go:33:2",
	}
	compareSlices(t, start, startAt)
	compareSlices(t, stop, stopAt)
}
