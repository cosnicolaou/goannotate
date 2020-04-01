package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"strings"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/text/edit"
	"github.com/cosnicolaou/goannotate/locate"
	"golang.org/x/tools/go/packages"
)

var (
	ConfigFileFlag string
	VerboseFlag    bool
	ProgressFlag   bool
)

func init() {
	flag.StringVar(&ConfigFileFlag, "config-file", "config.yaml", "yaml configuration file")
	flag.BoolVar(&VerboseFlag, "verbose", false, "display verbose debug info")
	flag.BoolVar(&ProgressFlag, "progress", true, "display progress info")
}

func trace(format string, args ...interface{}) {
	if !ProgressFlag {
		return
	}
	out := strings.Builder{}
	out.WriteString(time.Now().Format(time.StampMilli))
	out.WriteString(": ")
	out.WriteString(fmt.Sprintf(format, args...))
	fmt.Print(out.String())
}

func listPrefix(ctx context.Context, prefix string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", prefix)
	out, err := cmd.Output()
	if err != nil {
		cl := strings.Join(cmd.Args, ", ")
		exitErr := err.(*exec.ExitError)
		return nil, fmt.Errorf("failed to run %v: %v\n%s", cl, err, exitErr.Stderr)
	}
	parts := strings.Split(string(out), "\n")
	paths := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func listPackages(ctx context.Context, prefixes []string) ([]string, error) {
	packages := make([]string, 0, 100)
	errs := &errors.M{}
	for _, prefix := range prefixes {
		if strings.HasSuffix(prefix, "/...") {
			expanded, err := listPrefix(ctx, prefix)
			if err != nil {
				errs.Append(err)
				continue
			}
			packages = append(packages, expanded...)
			continue
		}
		packages = append(packages, prefix)
	}
	return packages, errs.Err()
}

func handleDebug(ctx context.Context, cfg Debug) (func(), error) {
	var cpu io.WriteCloser
	if filename := os.ExpandEnv(cfg.CPUProfile); len(filename) > 0 {
		var err error
		cpu, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		if err != nil {
			return func() {}, err
		}
		if err := pprof.StartCPUProfile(cpu); err != nil {
			cpu.Close()
			return func() {}, err
		}
		fmt.Printf("writing cpu profile to: %v\n", filename)
	}
	return func() {
		pprof.StopCPUProfile()
		if cpu != nil {
			cpu.Close()
		}
	}, nil
}

func handleSignals(fn func(), signals ...os.Signal) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, signals...)
	go func() {
		sig := <-sigCh
		fmt.Println("stopping on... ", sig)
		fn()
	}()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	flag.Parse()
	config, err := ConfigFromFile(ConfigFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		flag.Usage()
		os.Exit(1)
	}
	if VerboseFlag {
		fmt.Println(config.String())
	}

	cleanup, err := handleDebug(ctx, config.Debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure debuging/tracing: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	handleSignals(cancel, os.Interrupt, os.Kill)
	handleSignals(cleanup, os.Interrupt, os.Kill)

	trace("listing packages for interfaces...\n")
	errs := &errors.M{}
	interfaces, err := listPackages(ctx, config.Interfaces)
	errs.Append(err)
	trace("listing packages for functions...\n")
	functions, err := listPackages(ctx, config.Functions)
	errs.Append(err)
	trace("listing packages for implementations...\n")
	implementationPackages, err := listPackages(ctx, config.Packages)
	errs.Append(err)
	if err := errs.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to list interfaces, functions or packages: %v\n", err)
		os.Exit(1)
	}

	locator := locate.New(
		locate.Concurrency(config.Options.Concurrency),
		locate.Trace(trace),
		locate.IgnoreMissingFuctionsEtc(),
	)
	locator.AddInterfaces(interfaces...)
	locator.AddFunctions(functions...)
	locator.AddPackages(implementationPackages...)
	trace("locating...\n")
	errs.Append(locator.Do(ctx))
	if err := errs.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to locate functions and/or interface implementations: %v\n", err)
		os.Exit(1)
	}

	trace("interfaces...\n")
	locator.WalkInterfaces(func(
		fullname string,
		pkg *packages.Package,
		file *ast.File,
		decl *ast.TypeSpec,
		ifc *types.Interface) {
		trace("interface: %v @ %v\n", fullname, pkg.Fset.PositionFor(decl.Pos(), false))
	})

	trace("walking functions...\n")
	edits := map[string][]edit.Delta{}

	locator.WalkFunctions(func(fullname string,
		pkg *packages.Package,
		file *ast.File,
		fn *types.Func,
		decl *ast.FuncDecl,
		implements []string) {
		if config.Annotation.IgnoreEmptyFunctions && isEmpty(decl) {
			return
		}
		invovation, comment, err := annotationForFunc(&config.Annotation, pkg.Fset, fn, decl)
		if err != nil {
			errs.Append(err)
			return
		}
		if alreadyAnnotated(&config.Annotation, pkg.Fset, file, fn, decl, comment) {
			fmt.Printf("ALREADY DONE\n")
			return
		}

		lbrace := pkg.Fset.PositionFor(decl.Body.Lbrace, false)
		delta := edit.InsertString(lbrace.Offset+1, invovation+" // "+comment)
		edits[lbrace.Filename] = append(edits[lbrace.Filename], delta)
		trace("function: %v @ %v\n", fullname, lbrace)
	})

	importStatement := "\n" + `import "` + config.Annotation.Import + `"` + "\n"

	trace("walking files...\n")
	locator.WalkFiles(func(filename string,
		pkg *packages.Package,
		comments ast.CommentMap,
		file *ast.File) {
		_, end := locate.ImportBlock(file)
		pos := pkg.Fset.PositionFor(end, false)
		delta := edit.InsertString(pos.Offset, importStatement)
		edits[pos.Filename] = append(edits[pos.Filename], delta)
		trace("import: %v @ %v\n", importStatement, pos)
	})

	if err := errs.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to annotate: %v\n", err)
		os.Exit(1)
	}

	for file, edits := range edits {
		fmt.Printf("%v:\n", file)
		for _, edit := range edits {
			fmt.Printf("\t%s: %s\n", edit, edit.Text())
		}
		if err := editFile(ctx, file, edits); err != nil {
			err = fmt.Errorf("failed to edit file: %v: %v", file, err)
			errs.Append(err)
		}
		fmt.Println()
	}

	// edit each file concurrently.

	if err := errs.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to edit files: %v\n", err)
		os.Exit(1)
	}
}

func editFile(ctx context.Context, name string, deltas []edit.Delta) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	buf, err := ioutil.ReadFile(name)
	if err != nil {
		return err
	}
	buf = edit.Do(buf, deltas...)
	cmd := exec.CommandContext(ctx, "goimports")
	cmd.Stdin = bytes.NewBuffer(buf)
	out, err := cmd.Output()
	if err != nil {
		// This is most likely because the edit messed up the go code
		// and goimports is unhappy with it as its input. To help with
		// debugging write the edited code to a temp file.
		if VerboseFlag {
			if tmpfile, err := ioutil.TempFile("", "annotate-"); err == nil {
				io.Copy(tmpfile, bytes.NewBuffer(buf))
				tmpfile.Close()
				fmt.Printf("wrote modified contents of %v to %v\n", name, tmpfile.Name())
			}
		}
		return fmt.Errorf("%v: %v", strings.Join(cmd.Args, " "), err)
	}
	return ioutil.WriteFile(name, out, info.Mode().Perm())
}
