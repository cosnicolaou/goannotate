package findimpl_test

import (
	"context"
	"fmt"
	"go/build"
	"testing"

	"github.com/cosnicolaou/goannotate/findimpl"
)

func TestImpl(t *testing.T) {
	ctx := context.Background()
	here := "github.com/cosnicolaou/goannotate/findimpl/internal/"
	finder := findimpl.New()
	err := finder.AddInterfaces(ctx,
		here+"data.*",
	)
	if err != nil {
		t.Fatalf("findimpl.AddInterfaces: %v", err)
	}
	err = finder.FindInPkgs(ctx, build.Default, "github.com/cosnicolaou/goannotate/findimpl/...")
	fmt.Printf("%v\n", err)
	t.Fail()
}
