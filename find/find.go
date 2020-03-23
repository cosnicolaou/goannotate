// Package find provides a means of searching go code for implementations
// of specified go interfaces and functions.
package find

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
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"cloudeng.io/sync/errgroup"
)

func packageName(typ string) (pkgPath string, re *regexp.Regexp, err error) {
	compile := func(expr string) (*regexp.Regexp, error) {
		re, err = regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile regexp: %q: %w", expr, err)
		}
		return re, nil
	}
	idx := strings.LastIndex(typ, "/")
	if idx < 0 {
		re, err = compile(typ)
		return
	}
	dir := typ[:idx]
	tail := typ[idx+1:]
	idx = strings.Index(tail, ".")
	if idx < 0 {
		pkgPath = path.Join(dir, tail)
		re, err = compile(".*")
		return
	}
	pkgPath = path.Join(dir, tail[:idx])
	re, err = compile(tail[idx+1:])
	return
}

// T represents an implementation finder.
type T struct {
	fset *token.FileSet
	mu   sync.Mutex
	// GUARDED_BY(mu), indexed by <package-path>.<name>
	built           map[string]*build.Package
	parsed          map[string]*ast.Package
	checked         map[string]*types.Info
	interfaces      map[string]*types.Interface
	implemented     map[string][]string
	functions       map[string]*types.Func
	implementations map[string]*types.Func
	pos             map[string]token.Position
}

// New returns a new instance of T.
func New() *T {
	return &T{
		built:           make(map[string]*build.Package),
		parsed:          make(map[string]*ast.Package),
		checked:         make(map[string]*types.Info),
		interfaces:      make(map[string]*types.Interface),
		functions:       make(map[string]*types.Func),
		implementations: make(map[string]*types.Func),
		implemented:     make(map[string][]string),
		pos:             make(map[string]token.Position),
		fset:            token.NewFileSet(),
	}
}

func (t *T) build(pkgPath string) (*build.Package, error) {
	t.mu.Lock()
	built := t.built[pkgPath]
	t.mu.Unlock()
	if built != nil {
		return built, nil
	}
	context := build.Default
	built, err := context.Import(pkgPath, ".", build.FindOnly)
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
	// Only parse the main package and ignore the test package if present.
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
		IgnoreFuncBodies: false,
		Importer:         importer.ForCompiler(t.fset, "source", nil),
	}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	files := make([]*ast.File, 0, len(parsed.Files))
	for _, p := range parsed.Files {
		files = append(files, p)
	}
	pkg, err := config.Check(pkgPath, t.fset, files, info)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to typecheck %v: %w", pkgPath, err)
	}
	if !pkg.Complete() {
		return nil, nil, nil, fmt.Errorf("incomplete package %v", pkgPath)
	}
	t.mu.Lock()
	t.checked[pkgPath] = info
	t.mu.Unlock()
	return built, parsed, info, nil
}

// AddInterfaces adds interfaces representing an 'API" to the finder.
// The interface names are specified as fully qualified type names with a
// regular expression being accepted for the package local component.
// For example, all of the following match all interfaces in
// acme.com/a/b:
//   acme.com/a/b
//   acme.com/a/b.
//   acme.com/a/b..*
// Note that the . separator in the type name is not used as part of the
// regular expression. The following will match a subset of the
// interfaces:
//   acme.com/a/b.prefix
//   acme.com/a/b.thisInterface$
func (t *T) AddInterfaces(ctx context.Context, interfaces ...string) error {
	group, ctx := errgroup.WithContext(ctx)
	for _, ifc := range interfaces {
		pkgPath, ifcRE, err := packageName(ifc)
		if err != nil {
			return err
		}
		group.Go(func() error {
			return t.findInterfaces(ctx, pkgPath, ifcRE)
		})
	}
	return group.Wait()
}

// AddFunctions adds functions representing an 'API' to the finder.
// The function names are specified as fully qualified names with a
// regular expression being accepted for the package local component as per
// AddInterfaces.
func (t *T) AddFunctions(ctx context.Context, names ...string) error {
	group, ctx := errgroup.WithContext(ctx)
	for _, name := range names {
		pkgPath, nameRE, err := packageName(name)
		if err != nil {
			return err
		}
		group.Go(func() error {
			return t.findFunctions(ctx, pkgPath, nameRE)
		})
	}
	return group.Wait()
}

// APILocations returns the location of each interface and function that represents an API.
func (t *T) APILocations() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := strings.Builder{}
	for k := range t.interfaces {
		pos := t.pos[k]
		out.WriteString(k)
		out.WriteString(" interface ")
		out.WriteString(pos.String())
		out.WriteString("\n")
	}
	for k := range t.functions {
		pos := t.pos[k]
		out.WriteString(k)
		out.WriteString(" func ")
		out.WriteString(pos.String())
		out.WriteString("\n")
	}
	return out.String()
}

// AnnotationLocations returns the location of each function or method that would
// be annotated.
func (t *T) AnnotationLocations() string {
	out := strings.Builder{}
	t.WalkAnnotations(func(pos token.Position, name string, info *types.Info, fn *types.Func, implemented []string) {
		out.WriteString(name)
		if len(implemented) > 0 {
			out.WriteString(" implements ")
			sort.Strings(implemented)
			out.WriteString(strings.Join(implemented, ", "))
			out.WriteString(" at ")
		} else {
			out.WriteString(" func ")
		}
		out.WriteString(pos.String())
		out.WriteString("\n")
	})
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

func (t *T) findInterfaces(ctx context.Context, pkgPath string, ifcRE *regexp.Regexp) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	_, _, checked, err := t.buildParseAndCheck(pkgPath)
	if err != nil {
		return err
	}
	found := 0
	// Look in info.Defs for defined interfaces.
	for k, obj := range checked.Defs {
		if obj == nil || !k.IsExported() || !ifcRE.MatchString(k.Name) {
			continue
		}
		ifcType := isInterfaceType(obj.Type())
		if ifcType == nil {
			continue
		}
		if el := ifcType.NumEmbeddeds(); el > 0 {
			// Make sure to include embedded interfaces. To do so, gather
			// the names of the embedded interfaces and iterate over the
			// typed checked definitions to locate them.
			names := map[string]bool{}
			for i := 0; i < el; i++ {
				et := ifcType.EmbeddedType(i)
				named, ok := et.(*types.Named)
				if !ok {
					continue
				}
				obj := named.Obj()
				epkg := obj.Pkg()
				if epath := epkg.Path(); epath != pkgPath {
					// Treat the external embedded interface as if it was
					// directly requested.
					re, _ := regexp.Compile(obj.Name() + "$")
					t.findInterfaces(ctx, epath, re)
					continue
				}
				// Record the name of the locally defined embedded interfaces
				// and then look for them in the typed checked Defs.
				names[named.Obj().Name()] = true
			}
			for ek, eobj := range checked.Defs {
				if names[ek.Name] {
					fqn := pkgPath + "." + ek.Name
					ifcType := isInterfaceType(eobj.Type())
					if ifcType == nil {
						continue
					}
					t.mu.Lock()
					t.interfaces[fqn] = ifcType
					t.pos[fqn] = t.fset.Position(ek.Pos())
					t.mu.Unlock()
				}
			}
		}
		fqn := pkgPath + "." + k.Name
		found++
		t.mu.Lock()
		t.interfaces[fqn] = ifcType
		t.pos[fqn] = t.fset.Position(k.Pos())
		t.mu.Unlock()

	}
	if found == 0 {
		return fmt.Errorf("failed to find any exported interfaces in %v for %s", pkgPath, ifcRE)
	}
	return nil
}

func (t *T) findFunctions(ctx context.Context, pkgPath string, fnRE *regexp.Regexp) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	_, _, checked, err := t.buildParseAndCheck(pkgPath)
	if err != nil {
		return err
	}
	found := 0
	// Look in info.Defs for functions.
	for k, obj := range checked.Defs {
		if obj == nil || !k.IsExported() || !fnRE.MatchString(k.Name) {
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
		fqn := pkgPath + "." + k.Name
		found++
		t.mu.Lock()
		t.functions[fqn] = fn
		t.pos[fqn] = t.fset.Position(k.Pos())
		t.mu.Unlock()

	}
	if found == 0 {
		return fmt.Errorf("failed to find any exported functions in %v for %s", pkgPath, fnRE)
	}
	return nil
}

type sortByPos struct {
	name        string
	pos         token.Position
	fn          *types.Func
	implemented []string
}

// WalkAnnotations calls the supplied function for each function that is
// to be annotated. The annotations are orderd by their location positions,
// that is, by filename and then position within file.
func (t *T) WalkAnnotations(fn func(
	pos token.Position,
	name string,
	info *types.Info,
	fn *types.Func,
	implemented []string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sorted := make([]sortByPos, len(t.implementations)+len(t.functions))
	i := 0
	for k, v := range t.implementations {
		sorted[i] = sortByPos{
			name:        k,
			pos:         t.pos[k],
			fn:          v,
			implemented: t.implemented[k],
		}
		i++
	}
	for k, v := range t.functions {
		sorted[i] = sortByPos{
			name: k,
			pos:  t.pos[k],
			fn:   v,
		}
		i++
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].pos.Filename == sorted[j].pos.Filename {
			return sorted[i].pos.Offset < sorted[j].pos.Offset
		}
		return sorted[i].pos.Filename < sorted[j].pos.Filename
	})
	for _, loc := range sorted {
		info := t.checked[loc.fn.Pkg().Path()]
		fn(loc.pos, loc.name, info, loc.fn, loc.implemented)
	}
}
