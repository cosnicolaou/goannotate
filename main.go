package main

import (
	"context"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"strings"
	"time"

	"cloudeng.io/errors"
	"github.com/cosnicolaou/goannotate/locate"
)

var (
	ConfigFileFlag string
	VerboseFlag    bool
	ProgressFlag   bool
)

func init() {
	flag.StringVar(&ConfigFileFlag, "config-file", "goannotate.json", "json configuration file")
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
		fmt.Printf("writing cpu profile to: %v .. %v\n", filename, cpu)
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
	packages, err := listPackages(ctx, config.Packages)
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
	trace("locating interfaces...\n")
	errs.Append(locator.AddInterfaces(ctx, interfaces...))
	trace("locating functions...\n")
	errs.Append(locator.AddFunctions(ctx, functions...))
	trace("locating interface implementatations...\n")
	errs.Append(locator.Do(ctx, build.Default, packages...))

	if err := errs.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to locate functions and/or interface implementations: %v\n", err)
		os.Exit(1)
	}
	trace("walking files...\n")
	locator.WalkFiles(func(filename string, fset *token.FileSet, file *ast.File) {
		fmt.Printf("%v\n", filename)
	})
}
