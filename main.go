package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
)

var errNoGoBinOrPath = errors.New("goreinstall: unable to find a GOPATH or GOBIN from command (go env -json)")

func run() error {
	log.SetFlags(0)

	verbose := flag.Bool("v", false, "be verbose about operations")
	all := flag.Bool("a", false, "reinstall all binaries in GOBIN (eX: after go version update)")
	maxWorkers := flag.Int("t", runtime.NumCPU(), "max number of binaries to reinstall at once")
	update := flag.Bool("u", false, "update binaries if there is an update available")
	list := flag.Bool("l", false, "list all binaries found in GOBIN with extra version information")

	flag.CommandLine.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(),
			os.Args[0]+" reinstalls modules with new versions or when the go version is lower than the current one\n",
			"\nUsage: "+os.Args[0]+" [flags] <package(s) ...>\n",
			"Ex: "+os.Args[0]+" -a             // reinstall all binaries in GOBIN\n",
			"Ex: "+os.Args[0]+" -a -u          // reinstall all binaries and update if needed\n",
			"Ex: "+os.Args[0]+" goreinstall -u // reinstall goreinstall and update if needed\n",
			"\nFlags:",
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
		log.Print("Expected at least 1 package\n\n")
		flag.CommandLine.Usage()
		os.Exit(1)
		return nil
	}

	var (
		goBinVer string
		paths    []string
	)
	if *all {
		goEnv, err := getGoEnv(ctx)
		if err != nil {
			return fmt.Errorf("could not get GoEnv (%w)", err)
		}

		goBinVer = strings.ReplaceAll(goEnv.GoVersion, "go", "")

		paths, err = getAllGoBins(goEnv, *verbose)
		if err != nil {
			return fmt.Errorf("could not getAllGoBins (%w)", err)
		}
	} else {
		paths = flag.Args()
	}

	if *verbose {
		log.Println("going to try and check if we need to reinstall these binaries")

		for i := range paths {
			log.Println("\t" + paths[i])
		}
	}

	return reinstallBinaries(ctx, paths, *maxWorkers, *update, *verbose, goBinVer)
}

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}
