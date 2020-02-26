package findimpl

import (
	"context"
	"fmt"
	"go/build"
	"go/types"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/cosnicolaou/errors"
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

func (t *T) FindInPkgs(ctx context.Context, builder build.Context, packages ...string) error {
	pkgs, err := listPackages(ctx, packages)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	errs := errors.M{}
	var wg sync.WaitGroup
	wg.Add(len(pkgs))
	for _, pkg := range pkgs {
		go func(pkg string) {
			errs.Append(t.findInPkg(ctx, builder, pkg))
			wg.Done()
		}(pkg)
	}
	wg.Wait()
	return errs.Err()
}

func (t *T) findInPkg(ctx context.Context, builder build.Context, pkgPath string) error {
	_, _, checked, err := t.buildParseAndCheck(pkgPath)
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
				t.implementations[fn.String()] = fn
				t.implemented[fn.String()] = append(t.implemented[fn.String()], ifcPath)
				t.pos[fn.String()] = t.fset.Position(k.Pos())
			}
		}
		t.mu.Unlock()
	}
	return nil
}
