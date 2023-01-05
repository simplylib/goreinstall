package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/exp/slices"
)

var errNoGoBinOrPath = errors.New("goreinstall: unable to find a GOPATH or GOBIN from command (go env -json)")

func run() error {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)

	verbose := flag.Bool("v", false, "be verbose about operations")
	all := flag.Bool("a", false, "reinstall all binaries in GOBIN (ex: after go version update)")
	maxWorkers := flag.Int("t", runtime.NumCPU(), "max number of binaries to reinstall at once")
	update := flag.Bool("u", false, "update binaries if there is an update available")
	list := flag.Bool("l", false, "list all binaries found in GOBIN with extra version information")
	exclude := flag.String("e", "", "list of binaries to exclude from running against ex: \"goreinstall,gitsum\"")
	compiler := flag.String("c", "go", "name of binary to use instead of (go) for go commands")
	force := flag.Bool("f", false, "forcefully reinstall binaries even if not required")

	flag.CommandLine.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(),
			os.Args[0]+" reinstalls modules with new versions or when the go version is lower than the current one\n",
			"\nUsage: "+os.Args[0]+" [flags] <package(s) ...>\n\n",
			"Ex: "+os.Args[0]+" -a                // reinstall all binaries in GOBIN\n",
			"Ex: "+os.Args[0]+" -a -u             // reinstall all binaries and update if needed\n",
			"Ex: "+os.Args[0]+" goreinstall -u    // reinstall goreinstall and update if needed\n",
			"Ex: "+os.Args[0]+" -a -c \"go1.20rc2\" // reinstall all binaries if needed using go1.20rc2 command",
			"\nFlags:\n",
		)
		flag.CommandLine.PrintDefaults()
	}

	flag.Parse()

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	go func() {
		osSignal := make(chan os.Signal, 1)
		signal.Notify(osSignal, syscall.SIGTERM, os.Interrupt)

		s := <-osSignal
		log.Printf("Cancelling operations due to (%v)\n", s.String())
		cancelFunc()
		log.Println("operations cancelled")
	}()

	if *verbose {
		log.SetFlags(log.Ltime | log.Lshortfile)
	}

	if *list {
		return listCommand(ctx, os.Args)
	}

	if flag.NArg() == 0 && !*all {
		log.SetOutput(os.Stderr)
		log.Print("Expected at least 1 package\n\n")
		flag.CommandLine.Usage()
		return errors.New("")
	}

	var (
		goBinVer string
		paths    []string
	)
	if *all {
		goEnv, err := getGoEnv(ctx, *compiler)
		if err != nil {
			return fmt.Errorf("could not get GoEnv (%w)", err)
		}

		goBinVer = goEnv.GoVersion

		paths, err = getAllGoBins(goEnv, *verbose)
		if err != nil {
			return fmt.Errorf("could not getAllGoBins (%w)", err)
		}
	} else {
		paths = append(paths, flag.Args()...)
	}

	// strip excluded paths out
	if *exclude != "" {
		splitExcludes := strings.Split(strings.ReplaceAll(*exclude, " ", ""), ",")

		if *verbose {
			log.Printf("-e set, skipping (%v)", splitExcludes)
		}

		var strippedPaths []int
		for i := range paths {
			for j := range splitExcludes {
				if filepath.Base(paths[i]) != splitExcludes[j] {
					continue
				}
				strippedPaths = append(strippedPaths, i)
			}
		}

		for _, path := range strippedPaths {
			paths = slices.Delete(paths, path, path+1)
		}
	}

	if *verbose {
		log.Println("going to try and check if we need to reinstall these binaries")

		for i := range paths {
			log.Println("\t" + paths[i])
		}
	}

	gb := goBin{
		paths:        paths,
		workers:      *maxWorkers,
		compilerPath: *compiler,
		force:        *force,
		verbose:      *verbose,
		goBinVer:     goBinVer,
	}

	// update binaries before attempting to reinstall those binaries.
	// This will give us a chance to install binaries with current updates and current compiler,
	// preventing reinstalling just to recompile with current Go version ofr those select ones.
	if *update {
		err := gb.updateBinaries(ctx)
		if err != nil {
			return fmt.Errorf("could not updateBinaries (%w)", err)
		}
	}

	return gb.reinstallBinaries(ctx)
}

func main() {
	if err := run(); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatalln(err)
	}
}
