// Package findimpl provides a means of searching go code for implementations
// of specified go interfaces.
package findimpl

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/cosnicolaou/errors"
)

func packageName(typ string) (string, string) {
	sep := strings.LastIndex(typ, ".")
	if sep < 0 {
		return "", typ
	}
	return typ[:sep], typ[sep+1:]
}

// T represents an implementation finder.
type T struct {
	builder build.Context
	fset    *token.FileSet
	mu      sync.Mutex
	// GUARDED_BY(mu), indexed by package path
	built   map[string]*build.Package
	parsed  map[string]*ast.Package
	checked map[string]*types.Info
	ifcs    map[string]*types.Interface
	fns     map[string]*types.Func
	pos     map[string]token.Position
}

func (t *T) build(pkgPath string) (*build.Package, error) {
	t.mu.Lock()
	built := t.built[pkgPath]
	t.mu.Unlock()
	if built != nil {
		return built, nil
	}
	built, err := t.builder.Import(pkgPath, ".", 0)
	if err != nil {
		return nil, fmt.Errorf("failed to import %v: %w", pkgPath, err)
	}
	t.mu.Lock()
	t.built[pkgPath] = built
	t.mu.Unlock()
	return built, nil
}

func (t *T) buildAndParse(pkgPath string) (*build.Package, *ast.Package, error) {
	built, err := t.build(pkgPath)
	if err != nil {
		return nil, nil, err
	}
	t.mu.Lock()
	parsed := t.parsed[pkgPath]
	t.mu.Unlock()
	if parsed != nil {
		return built, parsed, nil
	}
	ignoreTestFiles := func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}
	// we only care about the single package in each package directory,
	// ignoring test packages etc.
	multi, err := parser.ParseDir(t.fset, built.Dir, ignoreTestFiles, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse dir %v: %w", built.Dir, err)
	}
	if len(multi) != 1 {
		pkgs := []string{}
		for k := range multi {
			pkgs = append(pkgs, k)
		}
		return nil, nil, fmt.Errorf("%v contains more than one package: %v", built.Dir, strings.Join(pkgs, ", "))
	}
	for _, v := range multi {
		parsed = v
	}
	t.mu.Lock()
	t.parsed[pkgPath] = parsed
	t.mu.Unlock()
	return built, parsed, nil
}

func (t *T) buildParseAndCheck(pkgPath string) (*build.Package, *ast.Package, *types.Info, error) {
	built, parsed, err := t.buildAndParse(pkgPath)
	if err != nil {
		return nil, nil, nil, err
	}
	t.mu.Lock()
	checked := t.checked[pkgPath]
	t.mu.Unlock()
	if checked != nil {
		return built, parsed, checked, nil
	}
	config := types.Config{
		IgnoreFuncBodies: true,
		Importer:         importer.ForCompiler(t.fset, runtime.Compiler, nil),
	}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	files := make([]*ast.File, 0, len(parsed.Files))
	for _, p := range parsed.Files {
		files = append(files, p)
	}
	if _, err := config.Check(pkgPath, t.fset, files, info); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to typecheck %v: %w", pkgPath, err)
	}
	t.mu.Lock()
	t.checked[pkgPath] = info
	t.mu.Unlock()
	return built, parsed, info, nil
}

// New returns a new instance of T.
func New(builder build.Context) *T {
	return &T{
		builder: builder,
		built:   make(map[string]*build.Package),
		parsed:  make(map[string]*ast.Package),
		checked: make(map[string]*types.Info),
		ifcs:    make(map[string]*types.Interface),
		fns:     make(map[string]*types.Func),
		pos:     make(map[string]token.Position),
		fset:    token.NewFileSet(),
	}
}

// AddInterfaces adds interfaces representing an 'API" to the finder.
// The interface name must be either a fully qualified type name as
// <package>.<interface> or <package>.* to include all interfaces in the
// package.
func (t *T) AddInterfaces(ctx context.Context, interfaces ...string) error {
	errs := errors.M{}
	for _, ifc := range interfaces {
		err := t.findInterfaces(ctx, t.builder, ifc)
		errs.Append(err)
	}
	return errs.Err()
}

// AddFunctions adds functions representing an 'API' to the finder.
// The function name must be either a fully qualified type name as
// <package>.<function> or <package>.* to include all exported functions in the
// package.
func (t *T) AddFunctions(ctx context.Context, names ...string) error {
	errs := errors.M{}
	for _, name := range names {
		err := t.findFunctions(ctx, t.builder, name)
		errs.Append(err)
	}
	return errs.Err()
}

// Locations will display the location of each interface that represents an API.
func (t *T) Locations() string {
	out := strings.Builder{}
	for k := range t.ifcs {
		pos := t.pos[k]
		out.WriteString(k)
		out.WriteString(" interface ")
		out.WriteString(pos.String())
		out.WriteString("\n")
	}
	for k := range t.fns {
		pos := t.pos[k]
		out.WriteString(k)
		out.WriteString(" func ")
		out.WriteString(pos.String())
		out.WriteString("\n")
	}
	return out.String()
}

func isInterfaceType(typ types.Type) *types.Interface {
	if types.IsInterface(typ) {
		switch v := typ.(type) {
		case *types.Named:
			it, ok := v.Underlying().(*types.Interface)
			if ok {
				return it
			}
		case *types.Interface:
			return v
		}
	}
	return nil
}

func (t *T) findInterfaces(ctx context.Context, builder build.Context, ifc string) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	pkgPath, ifcName := packageName(ifc)
	_, _, checked, err := t.buildParseAndCheck(pkgPath)
	if err != nil {
		return err
	}
	all := ifcName == "*"
	found := 0
	// Look in info.Defs for defined interfaces.
	for k, obj := range checked.Defs {
		if obj == nil || !k.IsExported() {
			continue
		}
		ifcType := isInterfaceType(obj.Type())
		if ifcType == nil {
			continue
		}
		if all || k.Name == ifcName {
			fqn := pkgPath + "." + k.Name
			found++
			t.mu.Lock()
			t.ifcs[fqn] = ifcType
			t.pos[fqn] = t.fset.Position(k.Pos())
			t.mu.Unlock()
			if !all {
				return nil
			}
		}
	}
	if all {
		if found == 0 {
			fmt.Errorf("failed to find any exported interfaces in %v", pkgPath)
		}
		return nil
	}
	return fmt.Errorf("failed to find exported interface %v", ifc)
}

func (t *T) findFunctions(ctx context.Context, builder build.Context, fn string) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	pkgPath, fnName := packageName(fn)
	_, _, checked, err := t.buildParseAndCheck(pkgPath)
	if err != nil {
		return err
	}
	all := fnName == "*"
	found := 0
	// Look in info.Defs for defined interfaces.
	// Look in info.Defs for functions.
	for k, obj := range checked.Defs {
		if obj == nil || !k.IsExported() {
			continue
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		if fn.Type().(*types.Signature).Recv() != nil {
			// either a method or an abstract function.
			continue
		}
		if all || k.Name == fnName {
			fqn := pkgPath + "." + k.Name
			found++
			t.mu.Lock()
			t.fns[fqn] = fn
			t.pos[fqn] = t.fset.Position(k.Pos())
			t.mu.Unlock()
			if !all {
				return nil
			}
		}
	}
	if all {
		if found == 0 {
			return fmt.Errorf("failed to find any exported functions in %v", pkgPath)
		}
		return nil
	}
	return fmt.Errorf("failed to find exported function %v", pkgPath)
}
