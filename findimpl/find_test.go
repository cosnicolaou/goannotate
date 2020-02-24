package findimpl_test

import (
	"fmt"
	"go/build"
	"sort"
	"strings"
	"testing"

	"github.com/cosnicolaou/goannotate/findimpl"
)

func TestIfc(t *testing.T) {
	here := "github.com/cosnicolaou/goannotate/findimpl/internal/"

	_, err := findimpl.New(build.Default,
		here+"data.nothere",
		here+"data.Ifc2",
	)
	if err == nil {
		t.Errorf("failed to detect missing interface")
	}
	f, err := findimpl.New(build.Default,
		here+"data.Ifc1",
		here+"data.Ifc2",
		here+"data.Ifc3",
	)
	if err != nil {
		t.Fatalf("failed to load interfaces: %v", err)
	}
	locations := strings.Split(f.Locations(), "\n")
	if got, want := len(locations), 4; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	sort.Strings(locations)
	for i, l := range locations {
		if i == 0 {
			// empty line comes first after sorting.
			if got, want := len(l), 0; got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
			continue
		}
		if got, want := l, fmt.Sprintf("%v%d", here+"data.Ifc", i); !strings.HasPrefix(got, want) {
			t.Errorf("%v doesn't start with %v", got, want)
		}
		if got, want := l, here+"data/interfaces.go:"+map[int]string{
			1: "3:6",
			2: "12:6",
			3: "16:6",
		}[i]; !strings.HasSuffix(got, want) {
			t.Errorf("got %v doesn't have suffix %v", got, want)
		}
	}
}
