// Package locate provides a means for obtaining the location of functions and
// implementations of interfaces in go source code, with a view to annotating
// that source code programmatically.
package locate

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

type body struct {
	begin, end token.Pos
}

// T represents the ability to locate functions and interface implementations.
type T struct {
	fset *token.FileSet
	mu   sync.Mutex
	// GUARDED_BY(mu), indexed by <package-path>.<name>
	built           map[string]*build.Package
	parsed          map[string]*ast.Package
	interfaces      map[string]*types.Interface
	interfaceDecl   map[string]*ast.TypeSpec
	implemented     map[string][]string
	functions       map[string]*types.Func
	implementations map[string]*types.Func
	fnDecl          map[string]*ast.FuncDecl
	// GUARDED_BY(mu), indexed by <package-path>
	checked map[string]*types.Info
	// GUARDED_BY(mu), indexed by filename
	files map[string]*ast.File
	dirty map[string]bool
}

// New returns a new instance of T.
func New() *T {
	return &T{
		built:           make(map[string]*build.Package),
		parsed:          make(map[string]*ast.Package),
		checked:         make(map[string]*types.Info),
		interfaces:      make(map[string]*types.Interface),
		interfaceDecl:   make(map[string]*ast.TypeSpec),
		functions:       make(map[string]*types.Func),
		implementations: make(map[string]*types.Func),
		implemented:     make(map[string][]string),
		fnDecl:          make(map[string]*ast.FuncDecl),
		files:           make(map[string]*ast.File),
		dirty:           make(map[string]bool),
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
	for _, f := range files {
		pos := t.fset.Position(f.Pos())
		t.files[pos.Filename] = f
	}
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
// regular expression. The following will match a subset of the interfaces:
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

// AddFunctions adds functions to the finder. The function names are specified
// as fully qualified names with a regular expression being accepted for the
// package local component as per AddInterfaces.
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

// Interfaces returns a string representation of all interface locations.
func (t *T) Interfaces() string {
	out := strings.Builder{}
	t.WalkInterfaces(func(name string, fset *token.FileSet, info *types.Info, decl *ast.TypeSpec, ifc *types.Interface) {
		out.WriteString(name)
		out.WriteString(" interface ")
		out.WriteString(fset.Position(decl.Pos()).String())
		out.WriteString("\n")
	})
	return out.String()
}

// Functions returns a string representation of all locations.
func (t *T) Functions() string {
	out := strings.Builder{}
	t.WalkFunctions(func(name string, fset *token.FileSet, info *types.Info, fn *types.Func, decl *ast.FuncDecl, implemented []string) {
		out.WriteString(name)
		if len(implemented) > 0 {
			out.WriteString(" implements ")
			sort.Strings(implemented)
			out.WriteString(strings.Join(implemented, ", "))
			out.WriteString(" at ")
		} else {
			out.WriteString(" func ")
		}
		out.WriteString(fset.Position(decl.Type.Func).String())
		out.WriteString("\n")
	})
	return out.String()
}

// Files returns a string representation of all files that contain interfaces
// or functions that were located..
func (t *T) Files() string {
	out := strings.Builder{}
	t.WalkFiles(func(name string, fileSet *token.FileSet, file *ast.File) {
		out.WriteString(name)
		out.WriteString(": ")
		out.WriteString(file.Name.String())
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

func (t *T) addInterface(pos token.Pos, pkg, name string, ifcType *types.Interface) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fqn := pkg + "." + name
	filename := t.fset.File(pos).Name()
	t.interfaces[fqn] = ifcType
	t.interfaceDecl[fqn] = findInterfaceDecl(name, t.files[filename])
	t.dirty[filename] = true
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
					if err := t.findInterfaces(ctx, epath, re); err != nil {
						return err
					}
					continue
				}
				// Record the name of the locally defined embedded interfaces
				// and then look for them in the typed checked Defs.
				names[named.Obj().Name()] = true
			}
			for ek, eobj := range checked.Defs {
				if names[ek.Name] {
					ifcType := isInterfaceType(eobj.Type())
					if ifcType == nil {
						continue
					}
					t.addInterface(ek.Pos(), pkgPath, ek.Name, ifcType)
				}
			}
		}
		found++
		t.addInterface(k.Pos(), pkgPath, k.Name, ifcType)

	}
	if found == 0 {
		return fmt.Errorf("failed to find any exported interfaces in %v for %s", pkgPath, ifcRE)
	}
	return nil
}

func (t *T) findFunctions(ctx context.Context, pkgPath string, fnRE *regexp.Regexp) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	_, pkg, checked, err := t.buildParseAndCheck(pkgPath)
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
		pos := t.fset.Position(k.Pos())
		t.fnDecl[fqn] = findFuncDecl(k.Name, pkg.Files[pos.Filename])
		t.dirty[pos.Filename] = true
		t.mu.Unlock()
	}
	if found == 0 {
		return fmt.Errorf("failed to find any exported functions in %v for %s", pkgPath, fnRE)
	}
	return nil
}

type sortByPos struct {
	name        string
	fn          *types.Func
	file        *ast.File
	pos         token.Position
	fnDecl      *ast.FuncDecl
	ifc         *types.Interface
	ifcDecl     *ast.TypeSpec
	implemented []string
}

func sorter(sorted []sortByPos) {
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].pos.Filename == sorted[j].pos.Filename {
			return sorted[i].pos.Offset < sorted[j].pos.Offset
		}
		return sorted[i].pos.Filename < sorted[j].pos.Filename
	})
}

// WalkFunctions calls the supplied function for each function location,
// ordered by filename and then position within file.
func (t *T) WalkFunctions(fn func(
	name string,
	fset *token.FileSet,
	info *types.Info,
	fn *types.Func,
	decl *ast.FuncDecl,
	implemented []string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sorted := make([]sortByPos, len(t.implementations)+len(t.functions))
	i := 0
	for k, v := range t.implementations {
		decl := t.fnDecl[k]
		sorted[i] = sortByPos{
			name:        k,
			pos:         t.fset.Position(decl.Type.Func),
			fn:          v,
			fnDecl:      decl,
			implemented: t.implemented[k],
		}
		i++
	}
	for k, v := range t.functions {
		decl := t.fnDecl[k]
		sorted[i] = sortByPos{
			name:   k,
			pos:    t.fset.Position(decl.Type.Func),
			fn:     v,
			fnDecl: decl,
		}
		i++
	}
	sorter(sorted)
	for _, loc := range sorted {
		info := t.checked[loc.fn.Pkg().Path()]
		fn(loc.name, t.fset, info, loc.fn, loc.fnDecl, loc.implemented)
	}
}

// WalkInterfaces calls the supplied function for each interface location,
// ordered by filename and then position within file.
func (t *T) WalkInterfaces(fn func(
	name string,
	fset *token.FileSet,
	info *types.Info,
	decl *ast.TypeSpec,
	ifc *types.Interface)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sorted := make([]sortByPos, len(t.interfaces))
	i := 0
	for k, v := range t.interfaces {
		decl := t.interfaceDecl[k]
		sorted[i] = sortByPos{
			name:    k,
			pos:     t.fset.Position(decl.Pos()),
			ifc:     v,
			ifcDecl: decl,
		}
		i++
	}
	sorter(sorted)
	for _, loc := range sorted {
		pkgPath := loc.name
		if idx := strings.LastIndex(pkgPath, "."); idx > 0 {
			pkgPath = pkgPath[:idx]
		}
		info := t.checked[pkgPath]
		fn(loc.name, t.fset, info, loc.ifcDecl, loc.ifc)
	}
}

// WalkFiles calls the supplied function for each file that contains
// a located interface or function, ordered by filename.
func (t *T) WalkFiles(fn func(
	name string,
	fileSet *token.FileSet,
	file *ast.File,
)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sorted := make([]sortByPos, 0, len(t.files))
	for k, v := range t.files {
		if !t.dirty[k] {
			continue
		}
		sorted = append(sorted, sortByPos{
			name: k,
			file: v,
		})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].name < sorted[j].name
	})
	for _, file := range sorted {
		fn(file.name, t.fset, file.file)
	}
}

// ImportBlock returns the start and end positions of an import statement
// or import block.
func ImportBlock(file *ast.File) (start, end token.Pos) {
	for _, d := range file.Decls {
		d, ok := d.(*ast.GenDecl)
		if !ok || d.Tok != token.IMPORT {
			break
		}
		if start == token.NoPos {
			start = d.Pos()
		}
		end = d.End()
	}
	return
}

func findFuncDecl(name string, file *ast.File) *ast.FuncDecl {
	for _, d := range file.Decls {
		d, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if name == d.Name.Name {
			return d
		}
	}
	return nil
}

func findInterfaceDecl(name string, file *ast.File) *ast.TypeSpec {
	for _, d := range file.Decls {
		d, ok := d.(*ast.GenDecl)
		if !ok || d.Tok != token.TYPE {
			continue
		}
		for _, spec := range d.Specs {
			typSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if typSpec.Name.Name == name {
				return typSpec
			}
		}
	}
	return nil
}
