// Package locate provides a means for obtaining the location of functions and
// implementations of interfaces in go source code, with a view to annotating
// that source code programmatically.
package locate

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"cloudeng.io/sync/errgroup"
)

type traceFunc func(string, ...interface{})

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

type interfaceDesc struct {
	path     string
	ifc      *types.Interface
	decl     *ast.TypeSpec
	position token.Position
}

type funcDesc struct {
	path       string
	fn         *types.Func
	decl       *ast.FuncDecl
	position   token.Position
	implements []string
}

// T represents the ability to locate functions and interface implementations.
type T struct {
	options options
	loader  *loader
	mu      sync.Mutex

	interfacePackages, functionPackages, implementationPackages []string
	loadPackages                                                []string

	// GUARDED_BY(mu), indexed by <package-path>.<name>
	interfaces map[string]interfaceDesc
	// GUARDED_BY(mu), indexed by types.Func.FullName() which includes
	// the receiver and hence is unique.
	functions map[string]funcDesc

	// GUARDED_BY(mu), indexed by filename
	dirty map[string]bool
}

type options struct {
	concurrency               int
	ignoreMissingFunctionsEtc bool
	trace                     func(string, ...interface{})
}

type Option func(*options)

// Concurrency sets the number of goroutines to use. 0 implies no limit.
func Concurrency(c int) Option {
	return func(o *options) {
		o.concurrency = c
	}
}

// Trace sets a trace function
func Trace(fn func(string, ...interface{})) Option {
	return func(o *options) {
		o.trace = fn
	}
}

// IgnoreMissingFuctionsEtc prevents errors due to packages not containing
// any exported matching interfaces and functions.
func IgnoreMissingFuctionsEtc() Option {
	return func(o *options) {
		o.ignoreMissingFunctionsEtc = true
	}
}

// TODO:
// options for 'ignoring' no interfaces or functions found in package.
// options for 'ignoring' errors due to cgo.

// New returns a new instance of T.
func New(options ...Option) *T {
	t := &T{
		interfaces: make(map[string]interfaceDesc),
		functions:  make(map[string]funcDesc),
		dirty:      make(map[string]bool),
	}
	t.loader = newLoader(t.trace)
	for _, fn := range options {
		fn(&t.options)
	}
	return t
}

func (t *T) trace(format string, args ...interface{}) {
	if t.options.trace == nil {
		return
	}
	t.options.trace(format, args...)
}

// AddInterfaces adds interfaces whose implementations are to be located.
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
func (t *T) AddInterfaces(interfaces ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.interfacePackages = append(t.interfacePackages, interfaces...)
	t.loadPackages = append(t.loadPackages, interfaces...)
}

// AddFunctions adds functions to be located. The function names are specified
// as fully qualified names with a regular expression being accepted for the
// package local component as per AddInterfaces.
func (t *T) AddFunctions(functions ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.functionPackages = append(t.functionPackages, functions...)
	t.loadPackages = append(t.loadPackages, functions...)

}

// AddPackages adds packages that will be searched for implementations
// of interfaces specified via AddInterfaces.
func (t *T) AddPackages(packages ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.implementationPackages = append(t.implementationPackages, packages...)
	t.loadPackages = append(t.loadPackages, packages...)

}

// Do locates implementations of previously added interfaces and functions.
func (t *T) Do(ctx context.Context) error {
	if err := t.loader.loadRegex(t.loadPackages...); err != nil {
		return err
	}
	group, ctx := errgroup.WithContext(ctx)
	group = errgroup.WithConcurrency(group, t.options.concurrency)
	for _, pkg := range packages {
		pkg := pkg
		group.GoContext(ctx, func() error {
			return t.findInPkg(ctx, pkg)
		})
	}
	return group.Wait()
}

type sortByPos struct {
	name    string
	pos     token.Position
	payload interface{}
}

func sorter(sorted []sortByPos) {
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].pos.Filename == sorted[j].pos.Filename {
			return sorted[i].pos.Offset < sorted[j].pos.Offset
		}
		return sorted[i].pos.Filename < sorted[j].pos.Filename
	})
}
