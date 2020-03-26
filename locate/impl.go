package locate

import (
	"context"
	"fmt"
	"go/build"
	"go/types"
	"os/exec"
	"sort"
	"strings"

	"cloudeng.io/sync/errgroup"
)

func listPackages(ctx context.Context, pkgs []string) ([]string, error) {
	args := append([]string{"list"}, pkgs...)
	cmd := exec.CommandContext(ctx, "go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to run go %v: %w", strings.Join(cmd.Args, " "), err)
	}
	lines := strings.Split(string(out), "\n")
	dedup := map[string]bool{}
	for _, l := range lines {
		if len(l) == 0 {
			continue
		}
		dedup[l] = true
	}
	list := make([]string, 0, len(dedup))
	for k := range dedup {
		list = append(list, k)
	}
	sort.Strings(list)
	return list, nil
}

// Do locates implementations of previously added interfaces in the provided
// packages.
func (t *T) Do(ctx context.Context, builder build.Context, packages ...string) error {
	pkgs, err := listPackages(ctx, packages)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	group, ctx := errgroup.WithContext(ctx)
	for _, pkg := range pkgs {
		pkg := pkg
		group.Go(func() error {
			return t.findInPkg(ctx, builder, pkg)
		})
	}
	return group.Wait()
}

func (t *T) findInPkg(ctx context.Context, builder build.Context, pkgPath string) error {
	checked, err := t.buildParseAndCheck(pkgPath)
	if err != nil {
		return err
	}
	// Look in info.Defs for functions.
	for k, obj := range checked.Defs {
		if obj == nil || !obj.Exported() {
			continue
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		sig := fn.Type().(*types.Signature)
		rcv := sig.Recv()
		if rcv == nil || isInterfaceType(rcv.Type()) != nil {
			// ignore functions and abstract methods
			continue
		}
		// a concrete method
		t.mu.Lock()
		for ifcPath, ifcType := range t.interfaces {
			if types.Implements(rcv.Type(), ifcType) {
				t.addFunction(k.Pos(), fn, ifcPath)
			}
		}
		t.mu.Unlock()
	}
	return nil
}
