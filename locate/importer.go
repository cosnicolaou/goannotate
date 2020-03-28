package locate

import (
	"sync"
	"fmt"

	"go/token"
	"go/types"
	"go/importer"
)

type cachingImporter struct {
	sync.Mutex
	pkgs map[string]*types.Package
	importer types.Importer
	trace func(string,...interface{})
}

func newCachingImporter(fset *token.FileSet, trace func(string, ...interface{})) types.Importer {
	return &cachingImporter{
		pkgs: 		make(map[string]*types.Package),
		importer: importer.ForCompiler(fset, "source", nil),
		trace: trace,
	}
}

func (ci *cachingImporter) Import(path string) (*types.Package, error) {
	ci.Lock()
	pkg := ci.pkgs[path]
	ci.Unlock()
	if pkg != nil {
		ci.trace("import: cached: %v\n",path)
		return pkg, nil
	}
	pkg, err := ci.importer.Import(path)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, fmt.Errorf("nil package importer for %v",path)
	}
	ci.Lock()
	ci.pkgs[path] = pkg
	ci.Unlock()
	ci.trace("import: %v\n",path)
	return pkg, nil
}