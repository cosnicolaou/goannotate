// Package findimpl provides a means of searching go code for implementations
// of specified go interfaces.
package findimpl

import (
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

	"github.com/cosnicolaou/errors"
)

func packageName(typ string) (string, string) {
	sep := strings.LastIndex(typ, ".")
	if sep < 0 {
		return "", typ
	}
	return typ[:sep], typ[sep+1:]
}

type T struct {
	ifcs map[string]*types.Interface
	pos  map[string]token.Position
}

func New(builder build.Context, interfaces ...string) (*T, error) {
	finder := &T{
		ifcs: make(map[string]*types.Interface),
		pos:  make(map[string]token.Position),
	}
	errs := errors.M{}
	for _, ifc := range interfaces {
		err := finder.findInterface(builder, ifc)
		errs.Append(err)
	}
	return finder, errs.Err()
}

func ignoreTestFiles(info os.FileInfo) bool {
	return !strings.HasSuffix(info.Name(), "_test.go")
}

// Locations will display the location of each interface.
func (t *T) Locations() string {
	out := strings.Builder{}
	for k := range t.ifcs {
		pos := t.pos[k]
		out.WriteString(k)
		out.WriteString(" ")
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

func (t *T) findInterface(builder build.Context, ifc string) error {
	// TODO: ensure that this code works correctly with modules. The go/...
	//       packages do not appear to be fully module aware yet.
	pkgPath, typName := packageName(ifc)
	pkg, err := builder.Import(pkgPath, ".", 0)
	if err != nil {
		return fmt.Errorf("failed to import %v from %v: %w", typName, pkgPath, err)
	}
	fset := token.NewFileSet()
	checker := types.Config{
		IgnoreFuncBodies: true,
		Importer:         importer.ForCompiler(fset, runtime.Compiler, nil),
	}
	pkgs, err := parser.ParseDir(fset, pkg.Dir, ignoreTestFiles, 0)
	for _, pkg := range pkgs {
		info := &types.Info{
			Defs: make(map[*ast.Ident]types.Object),
		}
		files := make([]*ast.File, 0, len(pkg.Files))
		for _, p := range pkg.Files {
			files = append(files, p)
		}
		_, err := checker.Check(pkgPath, fset, files, info)
		if err != nil {
			return fmt.Errorf("failed to typecheck %v: %w", pkgPath, err)
		}

		// Look in info.Defs for defined interfaces.
		for k, obj := range info.Defs {
			if obj == nil {
				continue
			}
			ifcType := isInterfaceType(obj.Type())
			if ifcType == nil {
				continue
			}
			if k.Name == typName {
				t.ifcs[ifc] = ifcType
				t.pos[ifc] = fset.Position(k.Pos())
				return nil
			}
		}
	}
	return fmt.Errorf("failed to find %v", ifc)
}
