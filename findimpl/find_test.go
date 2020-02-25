package findimpl_test

import (
	"context"
	"fmt"
	"go/build"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cosnicolaou/goannotate/findimpl"
)

func compareLocations(t *testing.T, value string, prefixes, suffixes []string) {
	_, file, line, _ := runtime.Caller(1)
	loc := fmt.Sprintf("%v:%v", filepath.Base(file), line)
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

const here = "github.com/cosnicolaou/goannotate/findimpl/internal/"

func TestInterfaces(t *testing.T) {
	ctx := context.Background()
	finder := findimpl.New(build.Default)
	err := finder.AddInterfaces(ctx,
		here+"data.nothere",
		here+"data.Ifc2",
	)
	if err == nil {
		t.Errorf("failed to detect missing interface")
	}
	err = finder.AddInterfaces(ctx,
		here+"data.Ifc1",
		here+"data.Ifc2",
	)
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	compareLocations(t, finder.Locations(), []string{
		here + "data.Ifc1",
		here + "data.Ifc2",
	}, []string{
		here + "data/interfaces.go:3:6",
		here + "data/interfaces.go:12:6",
	})
	err = finder.AddInterfaces(ctx,
		here+"data.*",
	)
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	compareLocations(t, finder.Locations(), []string{
		here + "data.Ifc1",
		here + "data.Ifc2",
		here + "data.Ifc3",
	}, []string{
		here + "data/interfaces.go:3:6",
		here + "data/interfaces.go:12:6",
		here + "data/interfaces.go:16:6",
	})
}

func TestFunctions(t *testing.T) {
	ctx := context.Background()
	finder := findimpl.New(build.Default)

	err := finder.AddFunctions(ctx, here+"data", "notthere")
	if err == nil {
		t.Errorf("failed to detect missing function")
	}

	err = finder.AddFunctions(ctx,
		here+"data.Fn2",
	)
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	compareLocations(t, finder.Locations(), []string{
		here + "data.Fn2 func",
	}, []string{
		here + "data/functions.go:11:6",
	})

	err = finder.AddFunctions(ctx,
		here+"data.*",
	)
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	compareLocations(t, finder.Locations(), []string{
		here + "data.Fn1 func",
		here + "data.Fn2 func",
	}, []string{
		here + "data/functions.go:7:6",
		here + "data/functions.go:11:6",
	})
}

func TestFunctionsAndInterfaces(t *testing.T) {
	ctx := context.Background()
	finder := findimpl.New(build.Default)
	err := finder.AddFunctions(ctx, here+"data.Fn2")
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	err = finder.AddInterfaces(ctx, here+"data.Ifc3")
	if err != nil {
		t.Fatalf("findimpl.New: %v", err)
	}
	compareLocations(t, finder.Locations(), []string{
		here + "data.Fn2 func",
		here + "data.Ifc3 interface",
	}, []string{
		here + "data/functions.go:11:6",
		here + "data/interfaces.go:16:6",
	})
}
